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

load(
    "@io_bazel_rules_go//go/private:context.bzl",
    "go_context",
)
load(
    "@io_bazel_rules_go//go/private:providers.bzl",
    "GoLibrary",
    "GoPath",
    "get_archive",
)
load(
    "@io_bazel_rules_go//go/private:common.bzl",
    "as_iterable",
)
load(
    "@io_bazel_rules_go//go/private:rules/rule.bzl",
    "go_rule",
)
load(
    "@io_bazel_rules_go//go/private/skylib/lib/shell.bzl",
    "shell",
)

def _effective_importpath(archive):
  # DO NOT SUBMIT: support vendoring
  return archive.importpath

def _go_path_impl(ctx):
  print("""
EXPERIMENTAL: the go_path rule is still very experimental
Please do not rely on it for production use, but feel free to use it and file issues
""")
  # Gather all packages.
  direct_archives = []
  transitive_archives = []
  for dep in ctx.attr.deps:
    archive = get_archive(dep)
    direct_archives.append(archive.data)
    transitive_archives.append(archive.transitive)
  archives = depset(direct = direct_archives, transitive = transitive_archives)

  # Build a map of files to write into the output directory.
  inputs = []
  manifest_entries = []
  for archive in as_iterable(archives):
    # DO NOT SUBMIT
    # TODO: detect duplicate packages
    # TODO: skip packages with missing imports
    # TODO: runfiles
    importpath = _effective_importpath(archive)
    out_prefix = "src/" + importpath + "/"
    for src in archive.orig_srcs:
      inputs.append(src)
      manifest_entry = "{'from': {}, 'to': {}}".format(
          shell.quote(src.path), shell.quote(out_prefix + src.basename))
      manifest_entries.append(manifest_entry)
    
  # Create a manifest for the builder.
  manifest = ctx.actions.declare_file(ctx.label.name + "~manifest")
  inputs.append(manifest)
  manifest_content = "[\n  " + ",\n  ".join(manifest_entries) + "\n]"
  ctx.actions.write(manifest, manifest_content)

  # Execute the builder
  if ctx.attr.mode == "archive":
    out = ctx.actions.declare_file(ctx.label.name + ".zip")
  else:
    out = ctx.actions.declare_directory(ctx.label.name + ".d")
  args = [
      "-manifest=" + manifest.path,
      "-out=" + out.path,
      "-mode=" + ctx.attr.mode,
  ]
  ctx.actions.run(
      outputs = [out],
      inputs = inputs,
      executable = ctx.file._go_path,
      args = args,
  )

  return [DefaultInfo(files = depset([out]))]

go_path = rule(
    _go_path_impl,
    attrs = {
        "deps": attr.label_list(providers = [GoLibrary]),
        "mode": attr.string(
            default = "copy",
            values = [
                "link",
                "copy",
                "archive",
            ],
        ),
        "_go_path": attr.label(
            default = "@io_bazel_rules_go//go/tools/builders:go_path",
            executable = True,
        ),
    },
)
