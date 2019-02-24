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

// compilepkg compiles a complete Go package from Go, C, and assembly files.  It
// supports cgo, coverage, and nogo. It is invoked by the Go rules as an action.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func compilePkg(args []string) error {
	// Parse arguments.
	args, err := readParamsFiles(args)
	if err != nil {
		return err
	}
	builderArgs, args := splitArgs(args)
	gcFlags, asmFlags := splitArgs(args)
	fs := flag.NewFlagSet("GoCompilePkg", flag.ExitOnError)
	goenv := envFlags(fs)
	var unfilteredSrcs, cgoArchivePaths multiFlag
	var deps compileArchiveMultiFlag
	var packagePath, nogoPath, packageListPath, outPath, outExportPath string
	var testFilter string
	fs.Var(&unfilteredSrcs, "src", ".go, .c, or .s file to be filtered and compiled")
	fs.Var(&deps, "arc", "Import path, package path, and file name of a direct dependency, separated by '='")
	fs.Var(&cgoArchivePaths, "cgoarc", "Path to a C/C++/ObjC archive to repack into the Go archive. May be repeated.")
	fs.StringVar(&packagePath, "p", "", "The package path (importmap) of the package being compiled")
	fs.StringVar(&nogoPath, "nogo", "", "The nogo binary. If unset, nogo will not be run.")
	fs.StringVar(&packageListPath, "package_list", "", "The file containing the list of standard library packages")
	fs.StringVar(&outPath, "o", "", "The output archive file to write")
	fs.StringVar(&outExportPath, "x", "", "The nogo facts file to write")
	fs.StringVar(&testFilter, "testfilter", "off", "Controls test package filtering")
	if err := fs.Parse(builderArgs); err != nil {
		return err
	}
	if err := goenv.checkFlags(); err != nil {
		return err
	}
	outPath = abs(outPath)

	// Filter sources.
	srcs, err := filterAndSplitFiles(unfilteredSrcs)
	if err != nil {
		return err
	}

	// TODO(jayconrod): remove -testfilter flag. The test action should compile
	// the main, internal, and external packages by calling compileArchive
	// with the correct sources for each.
	switch testFilter {
	case "off":
	case "only":
		testSrcs := make([]fileInfo, 0, len(srcs.goSrcs))
		for _, f := range srcs.goSrcs {
			if strings.HasSuffix(f.pkg, "_test") {
				testSrcs = append(testSrcs, f)
			}
		}
		srcs.goSrcs = testSrcs
	case "exclude":
		libSrcs := make([]fileInfo, 0, len(srcs.goSrcs))
		for _, f := range srcs.goSrcs {
			if !strings.HasSuffix(f.pkg, "_test") {
				libSrcs = append(libSrcs, f)
			}
		}
		srcs.goSrcs = libSrcs
	default:
		return fmt.Errorf("invalid test filter %q", testFilter)
	}

	return compileArchive(goenv, packagePath, srcs, deps, cgoArchivePaths, gcFlags, asmFlags, nogoPath, packageListPath, outPath, outExportPath)
}

func compileArchive(goenv *env, packagePath string, srcs archiveSrcs, deps []archive, cgoArchivePaths, gcFlags, asmFlags []string, nogoPath, packageListPath, outPath, outExportPath string) error {
	// TODO: run cgo commands
	// TODO: coverage
	// TODO: nogo
	workDir, cleanup, err := goenv.workDir()
	if err != nil {
		return err
	}
	defer cleanup()

	if len(srcs.goSrcs) == 0 {
		emptyPath := filepath.Join(workDir, "_empty.go")
		if err := ioutil.WriteFile(emptyPath, []byte("package empty\n"), 0666); err != nil {
			return err
		}
		srcs.goSrcs = append(srcs.goSrcs, fileInfo{
			filename: emptyPath,
			ext:      goExt,
			matched:  true,
			pkg:      "empty",
		})
		defer os.Remove(emptyPath)
	}

	// Check that the filtered sources don't import anything outside of
	// the standard library and the direct dependencies.
	_, stdImports, err := checkDirectDeps(srcs.goSrcs, deps, packageListPath)
	if err != nil {
		return err
	}

	// Build an importcfg file for the compiler.
	importcfgPath, err := buildImportcfgFileForCompile(deps, stdImports, goenv.installSuffix, filepath.Dir(outPath))
	if err != nil {
		return err
	}
	defer os.Remove(importcfgPath)

	// If there are assembly files, and this is go1.12+, generate symbol ABIs.
	asmHdrPath := ""
	if len(srcs.sSrcs) > 0 {
		asmHdrPath = filepath.Join(workDir, "go_asm.h")
	}
	symabisPath, err := buildSymabisFile(goenv, srcs.sSrcs, srcs.hSrcs, asmHdrPath)
	if symabisPath != "" {
		defer os.Remove(symabisPath)
	}
	if err != nil {
		return err
	}

	// Compile the filtered .go files.
	goSrcs := make([]string, len(srcs.goSrcs))
	for i, src := range srcs.goSrcs {
		goSrcs[i] = src.filename
	}
	if err := compileGo(goenv, goSrcs, packagePath, importcfgPath, asmHdrPath, symabisPath, gcFlags, outPath); err != nil {
		return err
	}

	// Compile the .s files, and pack them into the archive.
	if len(srcs.sSrcs) > 0 {
		includeSet := map[string]struct{}{
			filepath.Join(os.Getenv("GOROOT"), "pkg", "include"): struct{}{},
			workDir: struct{}{},
		}
		for _, hdr := range srcs.hSrcs {
			includeSet[filepath.Dir(hdr.filename)] = struct{}{}
		}
		includes := make([]string, len(includeSet))
		for inc := range includeSet {
			includes = append(includes, inc)
		}
		sort.Strings(includes)
		asmFlags := make([]string, 0, len(includeSet)*2)
		for _, inc := range includes {
			asmFlags = append(asmFlags, "-I", inc)
		}
		objPaths := make([]string, len(srcs.sSrcs))
		for i, sSrc := range srcs.sSrcs {
			objPaths[i] = filepath.Join(workDir, fmt.Sprintf("s%d.o", i))
			if err := asmFile(goenv, sSrc.filename, asmFlags, objPaths[i]); err != nil {
				return err
			}
		}
		if err := appendFiles(goenv, outPath, objPaths); err != nil {
			return err
		}
	}

	// Extract cgo archvies and re-pack them into the archive.
	if len(cgoArchivePaths) > 0 {
		names := map[string]struct{}{}
		var allObjPaths []string
		for _, cgoArchivePath := range cgoArchivePaths {
			objPaths, err := extractFiles(cgoArchivePath, workDir, names)
			if err != nil {
				return err
			}
			allObjPaths = append(allObjPaths, objPaths...)
		}
		if err := appendFiles(goenv, outPath, allObjPaths); err != nil {
			return err
		}
	}

	return nil
}

func compileGo(goenv *env, srcs []string, packagePath, importcfgPath, asmHdrPath, symabisPath string, gcFlags []string, outPath string) error {
	args := goenv.goTool("compile")
	args = append(args, "-p", packagePath, "-importcfg", importcfgPath, "-pack")
	if asmHdrPath != "" {
		args = append(args, "-asmhdr", asmHdrPath)
	}
	if symabisPath != "" {
		args = append(args, "-symabis", symabisPath)
	}
	args = append(args, gcFlags...)
	args = append(args, "-o", outPath)
	args = append(args, "--")
	args = append(args, srcs...)
	absArgs(args, []string{"-I", "-o", "-trimpath", "-importcfg"})
	return goenv.runCommand(args)
}
