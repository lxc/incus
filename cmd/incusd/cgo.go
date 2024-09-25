//go:build linux && cgo

package main

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden -Winit-self
// #cgo CFLAGS: -Wformat=2 -Wshadow -Wendif-labels -fasynchronous-unwind-tables
// #cgo CFLAGS: -pipe --param=ssp-buffer-size=4 -g -Wunused
// #cgo CFLAGS: -Werror=implicit-function-declaration
// #cgo CFLAGS: -Werror=return-type -Wendif-labels -Werror=overflow
// #cgo CFLAGS: -Wnested-externs -fexceptions
// #cgo CFLAGS: -I ../../shared/cgo
// #cgo pkg-config: lxc
// #cgo pkg-config: libcap
import "C"
