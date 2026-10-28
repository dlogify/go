//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package run

import (
	"strconv"
	"syscall"
)

var signalNames = map[syscall.Signal]string{
	syscall.SIGABRT: "SIGABRT", syscall.SIGALRM: "SIGALRM", syscall.SIGBUS: "SIGBUS", syscall.SIGCHLD: "SIGCHLD",
	syscall.SIGCONT: "SIGCONT", syscall.SIGFPE: "SIGFPE", syscall.SIGHUP: "SIGHUP", syscall.SIGILL: "SIGILL",
	syscall.SIGINT: "SIGINT", syscall.SIGIO: "SIGIO", syscall.SIGKILL: "SIGKILL", syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGPROF: "SIGPROF", syscall.SIGQUIT: "SIGQUIT", syscall.SIGSEGV: "SIGSEGV", syscall.SIGSTOP: "SIGSTOP",
	syscall.SIGSYS: "SIGSYS", syscall.SIGTERM: "SIGTERM", syscall.SIGTRAP: "SIGTRAP", syscall.SIGTSTP: "SIGTSTP",
	syscall.SIGTTIN: "SIGTTIN", syscall.SIGTTOU: "SIGTTOU", syscall.SIGURG: "SIGURG", syscall.SIGUSR1: "SIGUSR1",
	syscall.SIGUSR2: "SIGUSR2", syscall.SIGVTALRM: "SIGVTALRM", syscall.SIGWINCH: "SIGWINCH", syscall.SIGXCPU: "SIGXCPU",
	syscall.SIGXFSZ: "SIGXFSZ",
}

func signalName(s syscall.Signal) string {
	if name, ok := signalNames[s]; ok {
		return name
	}
	return "signal " + strconv.Itoa(int(s))
}
