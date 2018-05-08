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

load("@io_bazel_rules_go//go/private:common.bzl", "env_execute", "executable_extension")

_SDK_BUILD_TPL = """
load("@io_bazel_rules_go//go/private:rules/stdlib.bzl", "sdk_stdlib", "sdk_stdlib_set")

package(default_visibility = ["//visibility:public"])

filegroup(
    name = "go",
    srcs = glob(["bin/go", "bin/go.exe"]),
)

filegroup(
    name = "headers",
    srcs = glob(["pkg/include/**/*.h"]),
)

filegroup(
    name = "srcs",
    srcs = glob(["src/**"]),
)

filegroup(
    name = "tools",
    srcs = glob([
        "pkg/tool/**",
        "bin/*",
    ]),
)

{stdlib_rules}

sdk_stdlib_set(
    name = "stdlibs",
    deps = [
        {stdlib_names},
    ],
)

exports_files(["ROOT", "packages.txt"])
"""

_SDK_STDLIB_TPL = """sdk_stdlib(
    name = "{name}",
    root = "ROOT",
    srcs = [":srcs"],
    headers = [":headers"],
    libs = glob(
        ["pkg/{name}/**/*.a"],
        exclude = ["pkg/{name}/cmd/**"],
    ),
    tools = [":tools"],
    mode = "{name}",
)"""

def _go_host_sdk_impl(ctx):
  path = _detect_host_sdk(ctx)
  _local_sdk(ctx, path)
  _prepare(ctx)

go_host_sdk = repository_rule(
    _go_host_sdk_impl,
    environ = ["GOROOT"],
)

def _go_download_sdk_impl(ctx):
  if ctx.os.name == 'linux':
    host = "linux_amd64"
    res = ctx.execute(['uname', '-p'])
    if res.return_code == 0:
      uname = res.stdout.strip()
      if uname == 's390x':
        host = "linux_s390x"
      elif uname == 'ppc64le':
        host = "linux_ppc64le"
    # Default to amd64 when uname doesn't return a known value.
  elif ctx.os.name == 'mac os x':
    host = "darwin_amd64"
  elif ctx.os.name.startswith('windows'):
    host = "windows_amd64"
  else:
    fail("Unsupported operating system: " + ctx.os.name)
  sdks = ctx.attr.sdks
  if host not in sdks: fail("Unsupported host {}".format(host))
  filename, sha256 = ctx.attr.sdks[host]
  _remote_sdk(ctx, [url.format(filename) for url in ctx.attr.urls], ctx.attr.strip_prefix, sha256)
  _prepare(ctx)

go_download_sdk = repository_rule(
    _go_download_sdk_impl,
    attrs = {
        "sdks": attr.string_list_dict(),
        "urls": attr.string_list(default = ["https://storage.googleapis.com/golang/{}"]),
        "strip_prefix": attr.string(default = "go"),
    },
)

def _go_local_sdk_impl(ctx):
  _local_sdk(ctx, ctx.attr.path)
  _prepare(ctx)

go_local_sdk = repository_rule(
    _go_local_sdk_impl,
    attrs = {
        "path": attr.string(),
    },
)

def _prepare(ctx):
  # Create ROOT file. Used as a reference point for SDK stdlibs.
  ctx.file("ROOT", "")

  # Create packages.txt, a list of all the standard packages. Used by Gazelle
  # and other tools.
  package_set = {}
  src = ctx.path("src")
  prefix = str(src) + "/"
  dir_stack = [src]
  found_all = False
  for _ in [None] * 10000: # "while"
    if not dir_stack:
      found_all = True
      break
    d = dir_stack.pop()
    has_go = False
    for f in d.readdir():
      if f.basename in ("cmd", "internal", "vendor", "Makefile", "testdata", "README"):
        continue
      if f.basename.endswith(".go"):
        has_go = True
      elif f.basename.find(".") < 0:
        dir_stack.append(f)
    if has_go:
      package_name = str(d)[len(prefix):]
      package_set[package_name] = None
  if not found_all:
    fail("too many files in @go_sdk//:src to scan")
  package_set["testing/internal/testdeps"] = None # used by generated testmain
  packages = sorted(package_set.keys())
  ctx.file("packages.txt", "\n".join(packages))

  # Create BUILD.bazel. We need to make a list of stdlibs in the pkg directory.
  stdlib_names = []
  stdlib_rules = []
  for f in ctx.path("pkg").readdir():
    if f.basename.count("_") != 1:
      # Discard tool, include, and anything other than goos_goarch.
      # TODO(jayconrod): support _race, _shared, and others.
      continue
    stdlib_names.append(f.basename)
    stdlib_rules.append(_SDK_STDLIB_TPL.format(name = f.basename))

  build_content = _SDK_BUILD_TPL.format(
      stdlib_rules = "\n\n".join(stdlib_rules),
      stdlib_names = ",\n        ".join(['":{}"'.format(s) for s in stdlib_names]),
  )
  ctx.file("BUILD.bazel", build_content)

def _remote_sdk(ctx, urls, strip_prefix, sha256):
  ctx.download_and_extract(
      url = urls,
      stripPrefix = strip_prefix,
      sha256 = sha256,
  )

def _local_sdk(ctx, path):
  for entry in ["src", "pkg", "bin"]:
    ctx.symlink(path+"/"+entry, entry)

def _detect_host_sdk(ctx):
  root = "@invalid@"
  if "GOROOT" in ctx.os.environ:
    return ctx.os.environ["GOROOT"]
  res = ctx.execute(["go"+executable_extension(ctx), "env", "GOROOT"])
  if res.return_code:
    fail("Could not detect host go version")
  root = res.stdout.strip()
  if not root:
    fail("host go version failed to report it's GOROOT")
  return root
