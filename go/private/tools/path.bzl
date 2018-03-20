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

def _effective_importpath(archive):
  importpath = archive.importpath
  importmap = archive.importmap
  if importpath == "" or importmap == importpath:
    return importpath
  parts = importmap.split("/")
  if "vendor" not in parts:
    # Unusual case not handled by go build. Just return importpath.
    return importpath
  elif len(parts) > 2 and archive.label.workspace_root == "external/" + parts[0]:
    # Common case for importmap set by Gazelle in external repos.
    return "/".join(parts[1:])
  else:
    # Vendor directory somewhere in the main repo. Leave it alone.
    return importmap

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
  seen = {}
  for archive in as_iterable(archives):
    importpath = _effective_importpath(archive)
    if importpath == "":
      fail("Package does not have an importpath: {}".format(archive.label))
    if importpath in seen:
      print("""Duplicate package
Found {} in
  {}
  {}
""".format(importpath, str(archive.label), str(seen[importpath].label)))
    seen[importpath] = archive
    out_prefix = "src/" + importpath + "/"
    for src in archive.orig_srcs + archive.data_files:
      inputs.append(src)
      manifest_entry = struct(src = src.path, dst = out_prefix + src.basename)
      manifest_entries.append(manifest_entry.to_json())
    
  for src in ctx.files.data:
    inputs.append(src)
    manifest_entry = struct(src = src.path, dst = src.basename)
    manifest_entries.append(manifest_entry.to_json())

  # Create a manifest for the builder.
  manifest = ctx.actions.declare_file(ctx.label.name + "~manifest")
  inputs.append(manifest)
  manifest_content = "[\n  " + ",\n  ".join(manifest_entries) + "\n]"
  ctx.actions.write(manifest, manifest_content)

  # Execute the builder
  if ctx.attr.mode == "archive":
    out = ctx.actions.declare_file(ctx.label.name + ".zip")
  else:
    out = ctx.actions.declare_directory(ctx.label.name)
  args = [
      "-manifest=" + manifest.path,
      "-out=" + out.path,
      "-mode=" + ctx.attr.mode,
  ]
  ctx.actions.run(
      outputs = [out],
      inputs = inputs,
      mnemonic = "GoPath",
      executable = ctx.executable._go_path,
      arguments = args,
  )

  runfiles = ctx.runfiles(files = [out])
  return [DefaultInfo(
      files = depset([out]),
      runfiles = runfiles,
  )]

go_path = rule(
    _go_path_impl,
    attrs = {
        "deps": attr.label_list(providers = [GoLibrary]),
        "data": attr.label_list(
            allow_files = True,
            cfg = "data",
        ),
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
            cfg = "host",
        ),
    },
)
