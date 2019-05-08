// Copyright 2019 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	importerpkg "go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"strconv"
	"sync"
)

// stdIDPrefix is used to construct IDs for standard library packages.
// Each ID is prefix + importpath.
const stdIDPrefix = "@io_bazel_rules_go//:stdlib%"

// listPackages is used to unmarshal JSON packages written by "go list".
type listPackage struct {
	ImportPath, Export                                         string
	GoFiles, CompiledGoFiles                                   []string
	CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles []string
}

// stdPkgData collects information about packages in the standard library
// in a .zip file.
func stdPkgData(args []string) (err error) {
	// Parse command line arguments.
	builderArgs, toolArgs := splitArgs(args)
	fs := flag.NewFlagSet("GoStdPackageData", flag.ExitOnError)
	goenv := envFlags(fs)
	var outPath string
	fs.StringVar(&outPath, "o", "", "Output zip file to write")
	if err := fs.Parse(builderArgs); err != nil {
		return err
	}
	if err := goenv.checkFlags(); err != nil {
		return err
	}
	if outPath == "" {
		return errors.New("error: no output file specified")
	}
	if fs.NArg() != 0 {
		return errors.New("error: no positional arguments expected")
	}

	// Collect metadata on std packages.
	cache, err := ioutil.TempDir("", "bazelgocache")
	if err != nil {
		return err
	}
	os.Setenv("GOCACHE", cache)
	defer os.RemoveAll(cache)
	listArgs := goenv.goCmd("list", "-json")
	listArgs = append(listArgs, toolArgs...)
	listArgs = append(listArgs, "std")
	out, err := goenv.outputCommand(listArgs)
	if err != nil {
		return fmt.Errorf("error listing std packages: %v", err)
	}
	buf := bytes.NewBuffer(out)
	dec := json.NewDecoder(buf)
	var listPkgs []*listPackage
	listPkgsByPath := make(map[string]*listPackage)
	for dec.More() {
		lp := &listPackage{}
		if err := dec.Decode(lp); err != nil {
			return fmt.Errorf("error decoding 'go list' output: %v", err)
		}
		listPkgs = append(listPkgs, lp)
		listPkgsByPath[lp.ImportPath] = lp
	}

	// Load syntax and type information for each package.
	// Serialize that to json.
	mode := NeedName | NeedFiles | NeedCompiledGoFiles | NeedImports | NeedDeps | NeedExportsFile | NeedTypes | NeedSyntax | NeedTypesInfo | NeedTypesSizes
	jsonPkgs := make([][]byte, len(listPkgs))
	errs := make([]error, len(listPkgs))
	var wg sync.WaitGroup
	wg.Add(len(listPkgs))
	for i := range listPkgs {
		go func(i int) {
			defer wg.Done()
			lp := listPkgs[i]
			id := stdIDPrefix + lp.ImportPath
			var otherFiles []string
			for _, fs := range [][]string{lp.CgoFiles, lp.CFiles, lp.CXXFiles, lp.MFiles, lp.FFiles, lp.SFiles} {
				otherFiles = append(otherFiles, fs...)
			}
			lookup := func(importPath string) (id, filePath string) {
				return stdIDPrefix + importPath, listPkgsByPath[importPath].Export
			}
			pkg := loadPkgData(mode, id, lp.ImportPath, lp.GoFiles, lp.CompiledGoFiles, otherFiles, lp.Export, lookup)
			jsonPkgs[i], errs[i] = json.Marshal(&pkg)
			if errs[i] != nil {
				errs[i] = fmt.Errorf("error encoding package data for %s: %v", listPkgs[i].ImportPath, err)
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	// Create a zip archive with all the package data.
	zf, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := zf.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(zf)
	defer func() {
		if cerr := zw.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	addFile := func(i int) error {
		w, err := zw.Create(listPkgs[i].ImportPath + ".json")
		if err != nil {
			return err
		}
		_, err = w.Write(jsonPkgs[i])
		return err
	}
	for i := range listPkgs {
		if err := addFile(i); err != nil {
			return fmt.Errorf("error zipping package %s: %v", listPkgs[i].ImportPath, err)
		}
	}

	return nil
}

type lookupPkg func(importPath string) (id, filePath string)

func loadPkgData(mode LoadMode, id, pkgPath string, goFiles, compiledGoFiles, otherFiles []string, exportFile string, lookup lookupPkg) *Package {
	// Create a package and set information from the arguments.
	var err error
	pkg := &Package{ID: id}
	if mode&NeedName != 0 {
		pkg.PkgPath = pkgPath
	}
	if mode&NeedFiles != 0 {
		pkg.GoFiles = goFiles
		pkg.OtherFiles = otherFiles
	}
	if mode&NeedCompiledGoFiles != 0 {
		pkg.CompiledGoFiles = compiledGoFiles
	}
	if mode&NeedExportsFile != 0 {
		pkg.ExportFile = exportFile
	}

	// Parse files.
	parseMode := parser.AllErrors
	if mode&(NeedImports|NeedDeps|NeedTypes|NeedSyntax|NeedTypesInfo|NeedTypesSizes) == 0 {
		parseMode |= parser.PackageClauseOnly
	} else if mode&(NeedTypes|NeedSyntax|NeedTypesInfo|NeedTypesSizes) == 0 {
		parseMode |= parser.ImportsOnly
	}

	fset := token.NewFileSet()
	asts := make([]*ast.File, len(compiledGoFiles))
	for i, path := range compiledGoFiles {
		asts[i], err = parser.ParseFile(fset, path, nil, parseMode)
		if err == nil {
			continue
		}
		if err, ok := err.(scanner.ErrorList); ok {
			for _, e := range err {
				pkg.Errors = append(pkg.Errors, Error{
					Pos:  e.Pos.String(),
					Msg:  e.Msg,
					Kind: ParseError,
				})
			}
		} else {
			pkg.Errors = append(pkg.Errors, Error{
				Msg:  err.Error(),
				Kind: UnknownError,
			})
		}
	}

	if mode&NeedSyntax != 0 {
		pkg.Syntax = asts
	}

	// Load the package name. Make sure it's consistent across files.
	if mode&NeedName != 0 {
		var name string
		var namePos token.Pos
		for _, f := range asts {
			if name == "" {
				name = f.Name.Name
				namePos = f.Name.NamePos
			} else if name != f.Name.Name {
				pos := fset.Position(f.Name.NamePos)
				pkg.Errors = append(pkg.Errors, Error{
					Pos: pos.String(),
					Msg: fmt.Sprintf("package name %s doesn't match package name %s seen at %s", f.Name.Name, name, fset.Position(namePos)),
				})
			}
		}
		if name == "" {
			pkg.Errors = append(pkg.Errors, Error{
				Msg:  "no package name found",
				Kind: ListError,
			})
		} else {
			pkg.Name = name
		}
	}

	// Load imports. Loaded packages usually have a map from import paths to
	// Packages, but for serialization, we only need to write a stub Package
	// with an ID.
	if mode&NeedImports != 0 {
		imports := make(map[string]*Package)
		for _, f := range asts {
			for _, imp := range f.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					pkg.Errors = append(pkg.Errors, Error{
						Pos:  fset.Position(imp.Path.ValuePos).String(),
						Msg:  err.Error(),
						Kind: ListError,
					})
					continue
				} else if imports[path] != nil || path == "C" || path == "unsafe" {
					continue
				}
				id, _ := lookup(path)
				imports[path] = &Package{ID: id}
			}
		}
	}

	// Load type information.
	if mode&(NeedTypes|NeedTypesInfo) != 0 {
		importer := importerpkg.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
			_, filePath := lookup(path)
			return os.Open(filePath)
		})
		config := types.Config{
			Importer: importer,
			Error: func(err error) {
				terr := err.(types.Error)
				pkg.Errors = append(pkg.Errors, Error{
					Pos:  fset.Position(terr.Pos).String(),
					Msg:  terr.Msg,
					Kind: TypeError,
				})
			},
		}
		info := &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Uses:       make(map[*ast.Ident]types.Object),
			Defs:       make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Scopes:     make(map[ast.Node]*types.Scope),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
		}
		types, _ := config.Check(pkgPath, fset, asts, info)
		if mode&NeedTypes != 0 {
			pkg.Types = types
			pkg.Fset = fset
		}
		if mode&NeedTypesInfo != 0 {
			pkg.TypesInfo = info
		}
	}

	if mode&NeedTypesSizes != 0 {
		arch := os.Getenv("GOARCH")
		if arch == "" {
			arch = runtime.GOARCH
		}
		pkg.TypesSizes = types.SizesFor("gc", arch)
	}

	return pkg
}

// Definitions below this point copied from golang.org/x/tools/go/packages
// at 3b6f9c00 (2019-04-24). These definitions are needed for JSON marshaling.
//
// This binary is used to build other Go targets, and it cannot depend on
// packages outside of the Go standard library.

// A LoadMode specifies the amount of detail to return when loading.
// Higher-numbered modes cause Load to return more information,
// but may be slower. Load may return more information than requested.
type LoadMode int

const (
	// The following constants are used to specify which fields of the Package
	// should be filled when loading is done. As a special case to provide
	// backwards compatibility, a LoadMode of 0 is equivalent to LoadFiles.
	// For all other LoadModes, the bits below specify which fields will be filled
	// in the result packages.
	// WARNING: This part of the go/packages API is EXPERIMENTAL. It might
	// be changed or removed up until April 15 2019. After that date it will
	// be frozen.
	// TODO(matloob): Remove this comment on April 15.

	// ID and Errors (if present) will always be filled.

	// NeedName adds Name and PkgPath.
	NeedName LoadMode = 1 << iota

	// NeedFiles adds GoFiles and OtherFiles.
	NeedFiles

	// NeedCompiledGoFiles adds CompiledGoFiles.
	NeedCompiledGoFiles

	// NeedImports adds Imports. If NeedDeps is not set, the Imports field will contain
	// "placeholder" Packages with only the ID set.
	NeedImports

	// NeedDeps adds the fields requested by the LoadMode in the packages in Imports. If NeedImports
	// is not set NeedDeps has no effect.
	NeedDeps

	// NeedExportsFile adds ExportsFile.
	NeedExportsFile

	// NeedTypes adds Types, Fset, and IllTyped.
	NeedTypes

	// NeedSyntax adds Syntax.
	NeedSyntax

	// NeedTypesInfo adds TypesInfo.
	NeedTypesInfo

	// NeedTypesSizes adds TypesSizes.
	NeedTypesSizes
)

// A Package describes a loaded Go package.
type Package struct {
	// ID is a unique identifier for a package,
	// in a syntax provided by the underlying build system.
	//
	// Because the syntax varies based on the build system,
	// clients should treat IDs as opaque and not attempt to
	// interpret them.
	ID string

	// Name is the package name as it appears in the package source code.
	Name string

	// PkgPath is the package path as used by the go/types package.
	PkgPath string

	// Errors contains any errors encountered querying the metadata
	// of the package, or while parsing or type-checking its files.
	Errors []Error

	// GoFiles lists the absolute file paths of the package's Go source files.
	GoFiles []string

	// CompiledGoFiles lists the absolute file paths of the package's source
	// files that were presented to the compiler.
	// This may differ from GoFiles if files are processed before compilation.
	CompiledGoFiles []string

	// OtherFiles lists the absolute file paths of the package's non-Go source files,
	// including assembly, C, C++, Fortran, Objective-C, SWIG, and so on.
	OtherFiles []string

	// ExportFile is the absolute path to a file containing type
	// information for the package as provided by the build system.
	ExportFile string

	// Imports maps import paths appearing in the package's Go source files
	// to corresponding loaded Packages.
	Imports map[string]*Package

	// Types provides type information for the package.
	// Modes LoadTypes and above set this field for packages matching the
	// patterns; type information for dependencies may be missing or incomplete.
	// Mode LoadAllSyntax sets this field for all packages, including dependencies.
	Types *types.Package

	// Fset provides position information for Types, TypesInfo, and Syntax.
	// It is set only when Types is set.
	Fset *token.FileSet

	// IllTyped indicates whether the package or any dependency contains errors.
	// It is set only when Types is set.
	IllTyped bool

	// Syntax is the package's syntax trees, for the files listed in CompiledGoFiles.
	//
	// Mode LoadSyntax sets this field for packages matching the patterns.
	// Mode LoadAllSyntax sets this field for all packages, including dependencies.
	Syntax []*ast.File

	// TypesInfo provides type information about the package's syntax trees.
	// It is set only when Syntax is set.
	TypesInfo *types.Info

	// TypesSizes provides the effective size function for types in TypesInfo.
	TypesSizes types.Sizes
}

// An Error describes a problem with a package's metadata, syntax, or types.
type Error struct {
	Pos  string // "file:line:col" or "file:line" or "" or "-"
	Msg  string
	Kind ErrorKind
}

// ErrorKind describes the source of the error, allowing the user to
// differentiate between errors generated by the driver, the parser, or the
// type-checker.
type ErrorKind int

const (
	UnknownError ErrorKind = iota
	ListError
	ParseError
	TypeError
)

func (err Error) Error() string {
	pos := err.Pos
	if pos == "" {
		pos = "-" // like token.Position{}.String()
	}
	return pos + ": " + err.Msg
}

// flatPackage is the JSON form of Package
// It drops all the type and syntax fields, and transforms the Imports
//
// TODO(adonovan): identify this struct with Package, effectively
// publishing the JSON protocol.
type flatPackage struct {
	ID              string
	Name            string            `json:",omitempty"`
	PkgPath         string            `json:",omitempty"`
	Errors          []Error           `json:",omitempty"`
	GoFiles         []string          `json:",omitempty"`
	CompiledGoFiles []string          `json:",omitempty"`
	OtherFiles      []string          `json:",omitempty"`
	ExportFile      string            `json:",omitempty"`
	Imports         map[string]string `json:",omitempty"`
}

// MarshalJSON returns the Package in its JSON form.
// For the most part, the structure fields are written out unmodified, and
// the type and syntax fields are skipped.
// The imports are written out as just a map of path to package id.
// The errors are written using a custom type that tries to preserve the
// structure of error types we know about.
//
// This method exists to enable support for additional build systems.  It is
// not intended for use by clients of the API and we may change the format.
func (p *Package) MarshalJSON() ([]byte, error) {
	flat := &flatPackage{
		ID:              p.ID,
		Name:            p.Name,
		PkgPath:         p.PkgPath,
		Errors:          p.Errors,
		GoFiles:         p.GoFiles,
		CompiledGoFiles: p.CompiledGoFiles,
		OtherFiles:      p.OtherFiles,
		ExportFile:      p.ExportFile,
	}
	if len(p.Imports) > 0 {
		flat.Imports = make(map[string]string, len(p.Imports))
		for path, ipkg := range p.Imports {
			flat.Imports[path] = ipkg.ID
		}
	}
	return json.Marshal(flat)
}

// UnmarshalJSON reads in a Package from its JSON format.
// See MarshalJSON for details about the format accepted.
func (p *Package) UnmarshalJSON(b []byte) error {
	flat := &flatPackage{}
	if err := json.Unmarshal(b, &flat); err != nil {
		return err
	}
	*p = Package{
		ID:              flat.ID,
		Name:            flat.Name,
		PkgPath:         flat.PkgPath,
		Errors:          flat.Errors,
		GoFiles:         flat.GoFiles,
		CompiledGoFiles: flat.CompiledGoFiles,
		OtherFiles:      flat.OtherFiles,
		ExportFile:      flat.ExportFile,
	}
	if len(flat.Imports) > 0 {
		p.Imports = make(map[string]*Package, len(flat.Imports))
		for path, id := range flat.Imports {
			p.Imports[path] = &Package{ID: id}
		}
	}
	return nil
}

func (p *Package) String() string { return p.ID }
