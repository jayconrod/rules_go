# Copyright 2019 The Bazel Authors. All rights reserved.
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
    "@io_bazel_rules_go//go/private:mode.bzl",
    "link_mode_args",
)

def _archive(v):
    return "{}={}={}={}".format(
        v.data.importpath,
        v.data.importmap,
        v.data.file.path,
        v.data.export_file.path if v.data.export_file else "",
    )

def emit_compilepkg(
        go,
        sources = None,
        cover = None,
        importpath = "",
        importmap = "",
        archives = [],
        cgo_archives = [],
        out_lib = None,
        out_export = None,
        gc_goopts = [],
        testfilter = None):  # TODO: remove when test action compiles packages
    if sources == None:
        fail("sources is a required parameter")
    if out_lib == None:
        fail("out_lib is a required parameter")

    inputs = (sources + [go.package_list] +
              [archive.data.file for archive in archives] +
              cgo_archives +
              go.sdk.tools + go.sdk.headers + go.stdlib.libs)
    outputs = [out_lib]

    builder_args = go.builder_args(go, "compilepkg")
    builder_args.add_all(sources, before_each = "-src")
    if cover and go.coverdata:
        inputs.append(go.coverdata.data.file)
        builder_args.add("-arc", _archive(go.coverdata))
        builder_args.add("-cover_mode", "set")
        builder_args.add_all(cover, before_each = "-cover")
    builder_args.add_all(archives, before_each = "-arc", map_each = _archive)
    builder_args.add_all(cgo_archives, before_each = "-cgoarc")
    if importpath:
        builder_args.add("-importpath", importpath)
    if importmap:
        builder_args.add("-p", importmap)
    builder_args.add("-package_list", go.package_list)
    builder_args.add("-o", out_lib)
    if go.nogo:
        builder_args.add("-nogo", go.nogo)
        builder_args.add("-x", out_export)
        inputs.append(go.nogo)
        inputs.extend([archive.data.export_file for archive in archives if archive.data.export_file])
        outputs.append(out_export)
    if testfilter:
        builder_args.add("-testfilter", testfilter)

    gc_args = go.tool_args(go)
    gc_args.add_all([
        go._ctx.expand_make_variables("gc_goopts", f, {})
        for f in gc_goopts
    ])
    gc_args.add("-trimpath", ".")
    if go.mode.race:
        gc_args.add("-race")
    if go.mode.msan:
        gc_args.add("-msan")
    if go.mode.debug:
        gc_args.add_all(["-N", "-l"])
    gc_args.add_all(go.toolchain.flags.compile)
    gc_args.add_all(link_mode_args(go.mode))
        
    go.actions.run(
        inputs = inputs,
        outputs = outputs,
        mnemonic = "GoCompilePkg",
        executable = go.toolchain._builder,
        arguments = [builder_args, "--", gc_args],
        env = go.env,
    )
