// Copyright 2017 The Bazel Authors. All rights reserved.
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

// pack copies an .a file and appends a list of .o files to the copy using
// go tool pack. It is invoked by the Go rules as an action.
package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
)

func run(args []string) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	gotool := flags.String("gotool", "", "Path to the go tool")
	ar := flags.String("ar", "", "Path to the archive tool")
	inArchive := flags.String("in", "", "Path to input archive")
	outArchive := flags.String("out", "", "Path to output archive")
	objects := multiFlag{}
	flags.Var(&objects, "obj", "Object to append (may be repeated)")
	archive := flags.String("arc", "", "Archive to append (at most one)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if err := copyFile(*inArchive, *outArchive); err != nil {
		return err
	}

	if *archive != "" {
		archiveObjects, err := listFiles(*ar, *archive)
		if err != nil {
			return err
		}
		cmd := exec.Command(*ar, "x", *archive)
		if err := cmd.Run(); err != nil {
			return err
		}
		objects = append(objects, archiveObjects...)
	}

	packArgs := append([]string{"tool", "pack", "r", *outArchive}, objects...)
	cmd := exec.Command(*gotool, packArgs...)
	return cmd.Run()
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func copyFile(inPath, outPath string) error {
	inFile, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer inFile.Close()
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()
	_, err = io.Copy(outFile, inFile)
	return err
}

func listFiles(ar, archive string) ([]string, error) {
	cmd := exec.Command(ar, "t", archive)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
