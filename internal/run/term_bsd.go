//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package run

import "syscall"

const ioctlGetTermios = syscall.TIOCGETA
