# Copyright 2014 The Bazel Authors. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

load("@io_bazel_rules_go//go/private:mode.bzl", "mode_string")

GoLibrary = provider()
"""See go/providers.rst#GoLibrary for full documentation."""

GoPath = provider()

GoEmbed = provider()
"""See go/providers.rst#GoEmbed for full documentation."""

GoArchive = provider()
"""See go/providers.rst#GoArchive for full documentation."""

CgoInfo = provider()
GoStdLib = provider()

def MakeGoLibrary(label, importpath, direct, transitive, srcs, cover_vars, runfiles):
  return GoLibrary(
      label = _check(label, "label", "Label"),
      importpath = _check(importpath, "importpath", "string"),
      direct = _check(direct, "direct", "depset", "struct"),
      transitive = _check(transitive, "transitive", "depset", "struct"),
      srcs = _check(srcs, "srcs", "depset", "File"),
      cover_vars = _check(cover_vars, "cover_vars", "tuple", "string"),
      runfiles = _check(runfiles, "runfiles", "runfiles"),
  )

def MakeGoEmbed(srcs, build_srcs, deps, cover_vars, cgo_info, gc_goopts):
  return GoEmbed(
      srcs = _check(srcs, "srcs", "depset", "File"),
      build_srcs = _check(build_srcs, "build_srcs", "depset", "File"),
      deps = _check(deps, "deps", "depset", "struct"),
      cover_vars = _check(cover_vars, "cover_vars", "tuple", "string"),
      cgo_info = _check(cgo_info, "cgo_info", "struct", opt = True),
      gc_goopts = _check(gc_goopts, "gc_goopts", "tuple", "string"),
  )

def MakeGoArchive(mode, file, searchpath, library, embed, direct, transitive):
  return GoArchive(
      mode = _check(mode, "mode", "struct"),
      file = _check(file, "file", "File"),
      searchpath = _check(searchpath, "searchpath", "string"),
      library = _check(library, "library", "struct"), # GoLibrary
      embed = _check(embed, "embed", "struct"), # GoEmbed
      direct = _check(direct, "direct", "depset", "struct"), # GoArchive
      transitive = _check(transitive, "transitive", "depset", "struct"), # GoArchive
  )

def _check(value, name, type_, elem_type = None, opt = False):
  if type(value) != type_ and not (opt and value == None):
    fail("{} has type {} ; want {}".format(name, type(value), type_))
  if elem_type != None and bool(value):
    for e in value:
      if type(e) != elem_type:
        fail("{} elements have type {} ; want {}".format(name, type(e), elem_type))
      break
  return value
