/* Copyright 2018 The Bazel Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package go_path

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	copyPath string
)

var files = []string{
	"extra.txt",
	"src/",
	"-src/example.com/repo/cmd/bin/bin",
	"src/example.com/repo/cmd/bin/bin.go",
	"src/example.com/repo/pkg/lib/lib.go",
	"src/example.com/repo/pkg/lib/data.txt",
	"src/example.com/repo/vendor/example.com/repo2/vendored.go",
}

func TestMain(m *testing.M) {
	flag.StringVar(&copyPath, "copy_path", "", "path to copied go_path")
	flag.Parse()
	os.Exit(m.Run())
}

func TestCopyPath(t *testing.T) {
	if copyPath == "" {
		t.Fatal("-copy_path not set")
	}
	checkPath(t, copyPath, files, os.FileMode(0))
}

// checkPath checks that dir contains a list of files. files is a list of
// slash-separated paths relative to dir. Files that start with "-" should be
// absent. Files that end with "/" should be directories. Other files should
// be of fileType.
func checkPath(t *testing.T, dir string, files []string, fileType os.FileMode) {
	for _, f := range files {
		wantType := fileType
		wantAbsent := false
		if strings.HasPrefix(f, "-") {
			f = f[1:]
			wantAbsent = true
		}
		if strings.HasSuffix(f, "/") {
			wantType = os.ModeDir
		}
		path := filepath.Join(dir, filepath.FromSlash(f))
		st, err := os.Stat(path)
		if wantAbsent {
			if _, err := os.Stat(path); err == nil {
				t.Errorf("found %s: should not be present", f)
			} else if !os.IsNotExist(err) {
				t.Error(err)
			}
		} else {
			if err != nil {
				if os.IsNotExist(err) {
					t.Errorf("%s is missing", f)
				} else {
					t.Error(err)
				}
				continue
			}
			gotType := st.Mode() & os.ModeType
			if gotType != wantType {
				t.Errorf("%s: got type %s; want type %s", f, gotType, wantType)
			}
		}
	}
}
