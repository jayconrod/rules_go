// Copyright 2020 The Bazel Authors. All rights reserved.
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
	"io"
	"log"
	"os"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

func main() {
	filename, err := bazel.Runfile("tests/core/runfiles/action_in.txt")
	if err != nil {
		log.Fatal(err)
	}
	fd, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer fd.Close()
	if _, err := io.Copy(os.Stdout, fd); err != nil {
		log.Fatal(err)
	}
}
