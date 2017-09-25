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
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func run(args []string) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	gotool := flags.String("gotool", "", "Path to the go tool")
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
		archiveFiles, err := extractFiles(*archive, "bsd")
		if err != nil {
			return err
		}
		archiveObjects := filterFileNames(archiveFiles)
		objects = append(objects, archiveObjects...)
	}

	return appendFiles(*gotool, *outArchive, objects)
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

const (
	arHeader = "!<arch>\n"
	entryLen = 60
)

func extractFiles(archive, format string) (files []string, err error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	header := make([]byte, len(arHeader))
	if _, err := io.ReadFull(r, header); err != nil || string(header) != arHeader {
		return nil, fmt.Errorf("%s: bad header", archive)
	}

	for {
		var name string
		var size int64
		switch format {
		case "bsd":
			name, size, err = readBSDEntry(r)
		case "gnu":
			name, size, err = readGNUEntry(r)
		default:
			return nil, fmt.Errorf("%s: unknown format: %s", archive, format)
		}
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}

		if err := extractFile(r, name, size); err != nil {
			return nil, err
		}
		files = append(files, name)
	}
}

func readBSDEntry(r io.Reader) (name string, size int64, err error) {
	var entry [entryLen]byte
	if _, err := io.ReadFull(r, entry[:]); err != nil {
		return "", 0, err
	}

	sizeField := strings.TrimSpace(string(entry[48:58]))
	size, err = strconv.ParseInt(sizeField, 10, 64)
	if err != nil {
		return "", 0, err
	}

	nameField := string(entry[:16])
	if !strings.HasPrefix(nameField, "#1/") {
		name = nameField
	} else {
		nameField = strings.TrimSpace(nameField[len("#1/"):])
		nameLen, err := strconv.ParseInt(nameField, 10, 64)
		if err != nil {
			return "", 0, err
		}
		nameBuf := make([]byte, nameLen)
		if _, err := io.ReadFull(r, nameBuf); err != nil {
			return "", 0, err
		}
		name = strings.TrimRight(string(nameBuf), "\x00")
		size -= nameLen
	}

	return name, size, err
}

func readGNUEntry(r io.Reader) (name string, size int64, err error) {
	panic("not implemented")
}

func extractFile(r *bufio.Reader, name string, size int64) error {
	w, err := os.Create(name)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.CopyN(w, r, size)
	if err != nil {
		return err
	}
	if size%2 != 0 {
		if _, err := r.ReadByte(); err != nil {
			return err
		}
	}
	return nil
}

func filterFileNames(names []string) []string {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasSuffix(name, ".o") {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func appendFiles(gotool, archive string, files []string) error {
	args := append([]string{"tool", "pack", "r", archive}, files...)
	cmd := exec.Command(gotool, args...)
	return cmd.Run()
}
