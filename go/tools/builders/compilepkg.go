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
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
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
	var unfilteredSrcs, coverSrcs, cgoArchivePaths multiFlag
	var deps compileArchiveMultiFlag
	var importPath, packagePath, nogoPath, packageListPath, coverMode, outPath, outFactsPath string
	var testFilter string
	fs.Var(&unfilteredSrcs, "src", ".go, .c, or .s file to be filtered and compiled")
	fs.Var(&coverSrcs, "cover", ".go file that should be instrumented for coverage (must also be a -src)")
	fs.Var(&deps, "arc", "Import path, package path, and file name of a direct dependency, separated by '='")
	fs.Var(&cgoArchivePaths, "cgoarc", "Path to a C/C++/ObjC archive to repack into the Go archive. May be repeated.")
	fs.StringVar(&importPath, "importpath", "", "The import path of the package being compiled. Not passed to the compiler, but may be displayed in debug data.")
	fs.StringVar(&packagePath, "p", "", "The package path (importmap) of the package being compiled")
	fs.StringVar(&nogoPath, "nogo", "", "The nogo binary. If unset, nogo will not be run.")
	fs.StringVar(&packageListPath, "package_list", "", "The file containing the list of standard library packages")
	fs.StringVar(&coverMode, "cover_mode", "", "The coverage mode to use. Empty if coverage instrumentation should not be added.")
	fs.StringVar(&outPath, "o", "", "The output archive file to write")
	fs.StringVar(&outFactsPath, "x", "", "The nogo facts file to write")
	fs.StringVar(&testFilter, "testfilter", "off", "Controls test package filtering")
	if err := fs.Parse(builderArgs); err != nil {
		return err
	}
	if err := goenv.checkFlags(); err != nil {
		return err
	}
	if importPath == "" {
		importPath = packagePath
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

	return compileArchive(goenv, importPath, packagePath, srcs, deps, cgoArchivePaths, coverMode, coverSrcs, gcFlags, asmFlags, nogoPath, packageListPath, outPath, outFactsPath)
}

func compileArchive(goenv *env, importPath, packagePath string, srcs archiveSrcs, deps []archive, cgoArchivePaths []string, coverMode string, coverSrcs, gcFlags, asmFlags []string, nogoPath, packageListPath, outPath, outFactsPath string) error {
	// TODO: run cgo commands
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
	goSrcs := make([]string, len(srcs.goSrcs))
	for i, src := range srcs.goSrcs {
		goSrcs[i] = src.filename
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

	// Instrument source files for coverage.
	if coverMode != "" {
		shouldCover := make(map[string]bool)
		for _, s := range coverSrcs {
			shouldCover[s] = true
		}

		for i, origSrc := range goSrcs {
			if !shouldCover[origSrc] {
				continue
			}

			srcName := origSrc
			if importPath != "" {
				srcName = path.Join(importPath, filepath.Base(origSrc))
			}

			coverVar := fmt.Sprintf("Cover_%s_%s", sanitizePathForIdentifier(importPath), sanitizePathForIdentifier(origSrc[:len(origSrc)-len(".go")]))
			coverSrc := filepath.Join(workDir, fmt.Sprintf("cover_%d.go", i))
			if err := instrumentForCoverage(goenv, origSrc, srcName, coverVar, coverMode, coverSrc); err != nil {
				return err
			}

			goSrcs[i] = coverSrc
		}
	}

	// Run nogo concurrently.
	var nogoChan chan error
	if nogoPath != "" {
		ctx, cancel := context.WithCancel(context.Background())
		nogoChan = make(chan error)
		go func() {
			nogoChan <- runNogo(ctx, nogoPath, goSrcs, deps, stdImports, packagePath, importcfgPath, outFactsPath)
		}()
		defer func() {
			if nogoChan != nil {
				cancel()
				<-nogoChan
			}
		}()
	}

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

	// Check results from nogo.
	if nogoChan != nil {
		err := <-nogoChan
		nogoChan = nil // no cancellation needed
		if err != nil {
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

func runNogo(ctx context.Context, nogoPath string, srcs []string, deps []archive, stdImports []string, packagePath, importcfgPath, outFactsPath string) error {
	args := []string{nogoPath}
	args = append(args, "-p", packagePath)
	args = append(args, "-importcfg", importcfgPath)
	for _, imp := range stdImports {
		args = append(args, "-stdimport", imp)
	}
	for _, dep := range deps {
		if dep.xFile != "" {
			args = append(args, "-fact", fmt.Sprintf("%s=%s", dep.importPath, dep.xFile))
		}
	}
	args = append(args, "-x", outFactsPath)
	args = append(args, srcs...)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return errors.New(out.String())
		} else {
			if out.Len() != 0 {
				fmt.Fprintln(os.Stderr, out.String())
			}
			return fmt.Errorf("error running nogo: %v", err)
		}
	}
	return nil
}

func sanitizePathForIdentifier(path string) string {
	r := strings.NewReplacer("/", "_", "-", "_", ".", "_")
	return r.Replace(path)
}
