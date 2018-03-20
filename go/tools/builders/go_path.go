// Copyright 2018 The Bazel Authors. All rights reserved.
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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
)

type mode int

const (
	invalidMode mode = iota
	copyMode
	linkMode
	archiveMode
)

func modeFromString(s string) (mode, error) {
	switch s {
	case "copy":
		return copyMode, nil
	case "link":
		return linkMode, nil
	case "archive":
		return archiveMode, nil
	default:
		return invalidMode, fmt.Errorf("invalid mode: %s", s)
	}
}

type manifestEntry struct {
	Src, Dst string
}

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	var manifest, out string
	flags := flag.NewFlagSet("go_path", flag.ContinueOnError)
	flags.StringVar(&manifest, "manifest", "", "name of json file listing files to include")
	flags.StringVar(&out, "out", "", "output file or directory")
	modeFlag := flags.String("mode", "", "copy, link, or archive")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if manifest == "" {
		return errors.New("-manifest not set")
	}
	if out == "" {
		return errors.New("-out not set")
	}
	if *modeFlag == "" {
		return errors.New("-mode not set")
	}
	mode, err := modeFromString(*modeFlag)
	if err != nil {
		return err
	}

	entries, err := readManifest(manifest)
	if err != nil {
		return err
	}

	switch mode {
	case copyMode:
		err = copyPath(out, entries)
	case linkMode:
		err = linkPath(out, entries)
	case archiveMode:
		err = archivePath(out, entries)
	}
	return err
}

func readManifest(path string) ([]manifestEntry, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading manifest: %v", err)
	}
	var entries []manifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("error unmarshalling manifest %s: %v", path, err)
	}
	return entries, nil
}

func copyPath(out string, manifest []manifestEntry) error {
	if err := os.MkdirAll(out, 0777); err != nil {
		return err
	}
	for _, entry := range manifest {
		dst := filepath.Join(out, filepath.FromSlash(entry.Dst))
		if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
			return err
		}
		srcFile, err := os.Open(entry.Src)
		if err != nil {
			return err
		}
		dstFile, err := os.Create(dst)
		if err != nil {
			srcFile.Close()
			return err
		}
		if _, err := io.Copy(dstFile, srcFile); err != nil {
			dstFile.Close()
			srcFile.Close()
			return err
		}
		dstFile.Close()
		if err := srcFile.Close(); err != nil {
			return err
		}
	}
	return nil
}

func linkPath(out string, manifest []manifestEntry) error {
	if err := os.MkdirAll(out, 0777); err != nil {
		return err
	}
	for _, entry := range manifest {
		dst := filepath.Join(out, filepath.FromSlash(entry.Dst))
		if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
			return err
		}
		if err := os.Symlink(entry.Src, dst); err != nil {
			return err
		}
	}
	return nil
}

func archivePath(out string, manifest []manifestEntry) (err error) {
	outFile, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() {
		if e := outFile.Close(); err == nil && e != nil {
			err = fmt.Errorf("error closing archive %s: %v", out, e)
		}
	}()
	outZip := zip.NewWriter(outFile)

	for _, entry := range manifest {
		srcFile, err := os.Open(entry.Src)
		if err != nil {
			return err
		}
		w, err := outZip.Create(entry.Dst)
		if err != nil {
			srcFile.Close()
			return err
		}
		if _, err := io.Copy(w, srcFile); err != nil {
			srcFile.Close()
			return err
		}
		srcFile.Close()
	}

	if err := outZip.Close(); err != nil {
		return fmt.Errorf("error constructing archive %s: %v", out, err)
	}
	return nil
}
