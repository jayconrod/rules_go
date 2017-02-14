package(
    default_visibility = ["//visibility:public"],
)

load("//go:def.bzl", "go_binary")

go_binary(
    name = "stamped_bin",
    srcs = ["stamped_bin.go"],
    stamp = True,
    deps = ["//buildstamp:go_default_library"],
)
