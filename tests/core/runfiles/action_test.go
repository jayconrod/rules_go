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

package action_test

import (
	"bytes"
	"flag"
	"io/ioutil"
	"testing"
)

func Test(t *testing.T) {
	name := flag.Arg(0)
	got, err := ioutil.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`ʕ◔ϖ◔ʔ`)
	if !bytes.Equal(got, want) {
		t.Errorf("got %q; want %q", got, want)
	}
}
