# Copyright 2016 The Bazel Go Rules Authors. All rights reserved.
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

load(
    "@io_bazel_rules_go//go/private:providers.bzl",
    "GoStdLib",
    "GoStdLibSet",
)
load(
    "@io_bazel_rules_go//go/private:context.bzl",
    "go_context",
)
load(
    "@io_bazel_rules_go//go/private:rules/rule.bzl",
    "go_rule",
)
load(
    "@io_bazel_rules_go//go/private:mode.bzl",
    "LINKMODE_C_SHARED",
    "go_mode_to_stdlib_mode",
)

def _stdlib_impl(ctx):
  go = go_context(ctx)
  stdlib_mode = go_mode_to_stdlib_mode(go.mode)

  root_file = go.declare_file(go, "ROOT")
  src_dir = go.declare_directory(go, "src")
  libs_dir = go.declare_directory(go, "pkg/" + stdlib_mode)
  tools_dir = go.declare_directory(go, "pkg/tools")
  headers_dir = go.declare_directory(go, "pkg/headers")
  
  go.actions.write(root_file, "")

  args = go.args(go)
  args.add(["-out", root_file.dirname])
  if go.mode.race:
    args.add("-race")
  if go.mode == LINKMODE_C_SHARED:
    args.add("-shared")
  args.add(["-filter_buildid", ctx.executable._filter_buildid_builder.path])
  env = go.env
  env.update({
      "CC": go.cgo_tools.compiler_executable,
      "CGO_CPPFLAGS": " ".join(go.cgo_tools.compiler_options),
      "CGO_CFLAGS": " ".join(go.cgo_tools.c_options),
      "CGO_LDFLAGS": " ".join(go.cgo_tools.linker_options),
  })
  inputs = (go.crosstool + [go.go] + go.sdk_tools +
            ctx.files._sdk_headers + ctx.files._sdk_srcs +
            [ctx.executable._filter_buildid_builder])
  outputs = [src_dir, libs_dir, tools_dir, headers_dir]
  go.actions.run(
      inputs = inputs,
      outputs = outputs,
      mnemonic = "GoStdLib",
      executable = ctx.executable._stdlib_builder,
      arguments = [args],
      env = env,
  )

  return [
      DefaultInfo(files = depset(outputs)),
      GoStdLib(
          root = root_file,
          srcs = [src_dir],
          headers = [headers_dir],
          libs = [libs_dir],
          tools = [tools_dir],
          mode = stdlib_mode,
      ),
  ]

stdlib = go_rule(
    _stdlib_impl,
    bootstrap = True,
    attrs = {
        "_stdlib_builder": attr.label(
            executable = True,
            cfg = "host",
            default = Label("@io_bazel_rules_go//go/tools/builders:stdlib"),
        ),
        "_filter_buildid_builder": attr.label(
            executable = True,
            cfg = "host",
            default = Label("@io_bazel_rules_go//go/tools/builders:filter_buildid"),
        ),
        "_sdk_headers": attr.label(
            allow_files = True,
            cfg = "data",
            default = Label("@go_sdk//:headers"),
        ),
        "_sdk_srcs": attr.label(
            allow_files = True,
            cfg = "data",
            default = Label("@go_sdk//:srcs"),
        ),
    },
)

def _sdk_stdlib_impl(ctx):
  return [GoStdLib(
      root = ctx.file.root,
      srcs = ctx.files.srcs,
      headers = ctx.files.headers,
      libs = ctx.files.libs,
      tools = ctx.files.tools,
      mode = ctx.attr.mode,
  )]

sdk_stdlib = rule(
    _sdk_stdlib_impl,
    attrs = {
        "root": attr.label(
            allow_single_file = True,
            mandatory = True,
        ),
        "srcs": attr.label_list(
            mandatory = True,
        ),
        "headers": attr.label_list(
            allow_files = [".h"],
            mandatory = True,
        ),
        "libs": attr.label_list(
            allow_files = [".a"],
            mandatory = True,
        ),
        "tools": attr.label_list(
            allow_files = True,
            mandatory = True,
        ),
        "mode": attr.string(mandatory = True),
    },
)

def _sdk_stdlib_set(ctx):
  return [GoStdLibSet(stdlibs = [d[GoStdLib] for d in ctx.attr.deps])]

sdk_stdlib_set = rule(
    _sdk_stdlib_set,
    attrs = {
        "deps": attr.label_list(providers = [GoStdLib]),
    },
)

