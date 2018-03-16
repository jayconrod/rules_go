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
	"bufio"
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
	From, To string
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
	flag.StringVar(&manifest, "manifest", "", "name of json file listing files to include")
	flag.StringVar(&out, "out", "", "output file or directory")
	modeFlag := flag.String("mode", "", "copy, link, or archive")
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

	var outDir String
	if mode == archiveMode {
		outDir, err := ioutil.TempDir("", "go_path")
		if err != nil {
			return err
		}
		defer os.RemoveAll(outDir)
	} else {
		outDir = out
		if err := os.MkdirAll(outDir, 0777); err != nil {
			return err
		}
	}

	for _, entry := range entries {
		from := filepath.FromSlash(entry.From)
		to := filepath.Join(outDir, filepath.FromSlash(entry.To))
		toDir := filepath.Dir(to)
		if err := os.MkdirAll(toDir, 0777); err != nil {
			return err
		}
		if mode == linkMode {
			rel, err := filepath.Rel(toDir, from)
			if err != nil {
				return err
			}
			if err := os.Symlink(from, to); err != nil {
				return err
			}
		} else {
			fromFile, err := os.Open(from)
			if err != nil {
				return err
			}
			defer fromFile.Close()
			toFile, err := os.Create(to)
			if err != nil {
				return err
			}
			if err := io.Copy(toFile, fromFile); err != nil {
				toFile.Close()
				return err
			}
			if err := toFile.Close(); err != nil {
				return err
			}
		}
	}
}

func readManifest(path string) ([]manifestEntry, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []manifestEntry
	if err := json.Unmarshal(manifestData, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func archivePath(out string, manifest []manifestEntry) (err error) {
	outFile, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() {
		if e := outFile.Close(); err == nil && e != nil {
			err = e
		}
	}()
	outBuffer := bufio.NewWriter(outFile)
	outZip := zip.NewWriter(outBuffer)

	for _, entry := range manifest {
		inFile, err := os.Open(entry.From)
		if err != nil {
			return err
		}
		w, err := outZip.Create(entry.To)
		if err != nil {
			inFile.Close()
			return err
		}
		if err := io.Copy(w, inFile); err != nil {
			inFile.Close()
			return err
		}
		inFile.Close()
	}

	if err := outZip.Close(); err != nil {
		return err
	}
	outFile = nil
}
