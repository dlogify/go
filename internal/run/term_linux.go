//go:build linux

package run

import "syscall"

const ioctlGetTermios = syscall.TCGETS
