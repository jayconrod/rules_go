# Copyright 2018 The Bazel Authors. All rights reserved.
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

load("@io_bazel_rules_go//go:def.bzl", "go_context", "go_rule")
load("@io_bazel_rules_go//go/private:providers.bzl", "GoStdLib")

def _sdk_test_impl(ctx):
  go = go_context(ctx)
  if go.stdlib.root != ctx.file._root:
    fail("go rules not built with SDK stdlib by default\ngot {}; want {}".format(
      go.stdlib.root, ctx.file._root))

  # emit a trivial passing script
  script = ctx.actions.declare_file(ctx.label.name + ".sh")
  ctx.actions.write(script, "")
  return [DefaultInfo(files = depset([script]))]

_sdk_test_script = go_rule(
    _sdk_test_impl,
    attrs = {
        "_root": attr.label(default = "@go_sdk//:ROOT", allow_single_file = True),
    },
)

def sdk_test(name):
  script_name = name + "_script"
  _sdk_test_script(
      name = script_name,
      tags = ["manual"],
  )
  native.sh_test(
      name = name,
      srcs = [script_name],
  )
       
