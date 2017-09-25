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

def emit_pack(ctx, go_toolchain, in_lib, out_lib, objects = (), archive = None):
  """Construct the command line for packing objects together.

  Args:
    ctx: The skylark Context.
    in_lib: the archive that should be copied and appended to.
    out_lib: the archive that should be produced
    objects: an iterable of object files to be added to the output archive file.
    archive: an optional archive file to be concatenated with the output
        archive file.
  """
  inputs = [in_lib] + go_toolchain.data.tools
    
  arguments = [
      "-gotool", go_toolchain.tools.go.path,
      "-in", in_lib.path,
      "-out", out_lib.path,
  ]
  inputs.extend(objects)
  for obj in objects:
    arguments.extend(["-obj", obj.path])

  if archive:
    inputs.append(archive)
    arguments.extend(["-arc", archive.path])

  ctx.action(
      inputs = inputs,
      outputs = [out_lib],
      mnemonic = "GoPack",
      executable = go_toolchain.tools.pack,
      arguments = arguments,
      env = go_toolchain.env,
  )
