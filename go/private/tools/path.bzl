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
    "EXPLICIT_PATH",
)
load(
    "@io_bazel_rules_go//go/private:providers.bzl",
    "GoArchive",
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
  manifest = {}   # maps destinations to sources
  for archive in as_iterable(archives):
    importpath = _effective_importpath(archive)
    if importpath == "":
      continue
    out_prefix = "src/" + importpath + "/"
    for src in archive.orig_srcs + archive.data_files:
      manifest[out_prefix + src.basename] = src
  for src in ctx.files.data:
    manifest[src.basename] = src
  inputs = manifest.values()

  # Create a manifest for the builder.
  manifest_file = ctx.actions.declare_file(ctx.label.name + "~manifest")
  inputs.append(manifest_file)
  manifest_entries = [struct(src = src.path, dst = dst).to_json()
                      for dst, src in manifest.items()]
  manifest_content = "[\n  " + ",\n  ".join(manifest_entries) + "\n]"
  ctx.actions.write(manifest_file, manifest_content)

  # Execute the builder
  if ctx.attr.mode == "archive":
    out = ctx.actions.declare_file(ctx.label.name + ".zip")
    out_path = out.path
    outputs = [out]
  elif ctx.attr.mode == "copy":
    out = ctx.actions.declare_directory(ctx.label.name)
    out_path = out.path
    outputs = [out]
  else:  # link
    outputs = [ctx.actions.declare_file(ctx.label.name + "/" + dst)
               for dst in manifest]
    tag = ctx.actions.declare_file(ctx.label.name + "/.tag")
    outputs.append(tag)
    out_path = tag.dirname
  if len(outputs) > 0:
    args = [
        "-manifest=" + manifest_file.path,
        "-out=" + out_path,
        "-mode=" + ctx.attr.mode,
    ]
    ctx.actions.run(
        outputs = outputs,
        inputs = inputs,
        mnemonic = "GoPath",
        executable = ctx.executable._go_path,
        arguments = args,
    )

  return [DefaultInfo(
      files = depset(outputs),
      runfiles = ctx.runfiles(files = outputs),
  )]
  # TODO: GoPath provider?

go_path = rule(
    _go_path_impl,
    attrs = {
        "deps": attr.label_list(providers = [GoArchive]),
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

def _effective_importpath(archive):
  if archive.pathtype != EXPLICIT_PATH:
    return ""
  importpath = archive.importpath
  importmap = archive.importmap
  if importpath.endswith("_test"): importpath = importpath[:-len("_test")]
  if importmap.endswith("_test"): importmap = importmap[:-len("_test")]
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
