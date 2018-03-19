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
	"testing"
)

var (
	copyPath string
)

func TestMain(m *testing.M) {
	flag.StringVar(&copyPath, "copy_path", "", "path to copied go_path")
	flag.Parse()
	os.Exit(m.Run())
}

func TestCopyPath(t *testing.T) {
	if copyPath == "" {
		t.Fatal("-copy_path not set")
	}
	checkPath(t, copyPath, []fileSpec{
		{path: "extra.txt"},
		{path: "src", ty: os.ModeDir},
		{path: "src/example.com/repo/cmd/bin/bin", ty: absent},
		{path: "src/example.com/repo/cmd/bin/bin.go"},
		{path: "src/example.com/repo/pkg/lib/lib.go"},
		{path: "src/example.com/repo/pkg/lib/data.txt"},
		// TODO(#1329):
		// {path: "src/example.com/repo/pkg/lib/internal_test.go"},
		// {path: "src/example.com/repo/pkg/lib/external_test.go"},
	})
}

const absent = os.ModeType // all type bits set

type fileSpec struct {
	path string
	ty   os.FileMode // 0 indicates a regular file.
}

func checkPath(t *testing.T, dir string, files []fileSpec) {
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.path))
		st, err := os.Stat(path)
		if f.ty == absent {
			if err == nil {
				t.Errorf("found %s; should not be present", f.path)
				continue
			}
			if !os.IsNotExist(err) {
				t.Error(err)
				continue
			}
		} else {
			if os.IsNotExist(err) {
				t.Errorf("%s is missing", f.path)
				continue
			}
			if err != nil {
				t.Error(err)
				continue
			}
			ty := st.Mode() & os.ModeType
			if ty != f.ty {
				t.Errorf("%s: got type %s; want type %s", ty, f.ty)
			}
		}
	}
}
