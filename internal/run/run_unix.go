//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package run

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"dlogify.com/sdk/go/internal/bridge"
	"dlogify.com/sdk/go/internal/record"
)

func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(ioctlGetTermios), uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

func cannotRun(command string, err error) int {
	reason, code := err.Error(), 126
	switch {
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
		reason, code = "command not found", 127
	case errors.Is(err, fs.ErrPermission):
		reason = "permission denied"
	}
	fmt.Fprintf(os.Stderr, "logify: cannot run %s: %s\n", command, reason)
	return code
}

// relay writes each chunk to our own stream first, unchanged, then parses a
// copy. If our reader goes away (EPIPE, as in `logify run -- cmd | head`), the
// child's pipe is closed too, so the child sees its reader go away as it
// would without logify run.
func relay(from, to *os.File, p *lockedParser, broken *atomic.Bool, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 64*1024)
	for {
		n, err := from.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if !broken.Load() {
				if _, werr := to.Write(chunk); werr != nil && errors.Is(werr, syscall.EPIPE) {
					broken.Store(true)
					from.Close()
				}
			}
			p.push(chunk)
		}
		if err != nil {
			return
		}
	}
}

func runCommand(client bridge.Client, command string, args []string) int {
	cmd := exec.Command(command, args...)
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil // like a shell: a command found through "." in PATH runs
	}
	cmd.Env = childEnv(os.Environ())
	cmd.Stdin = os.Stdin
	outR, outW, err := os.Pipe()
	if err != nil {
		return cannotRun(command, err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		return cannotRun(command, err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW

	// SIGPIPE is registered so a write to a closed stdout returns EPIPE
	// instead of ending logify run (the Go runtime's default for fds 1 and 2).
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return cannotRun(command, err)
	}
	outW.Close()
	errW.Close()

	stdout, stderr := newParser(client, "stdout"), newParser(client, "stderr")
	var readers sync.WaitGroup
	var brokenOut, brokenErr atomic.Bool
	readers.Add(2)
	go relay(outR, os.Stdout, stdout, &brokenOut, &readers)
	go relay(errR, os.Stderr, stderr, &brokenErr, &readers)

	stopTick := make(chan struct{})
	go func() {
		t := time.NewTicker(tickEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				stdout.tick()
				stderr.tick()
			case <-stopTick:
				return
			}
		}
	}()

	// Signals logify run received, forwarded or not: a child they end is not
	// reported. A terminal sends Ctrl-C and hang-up to the whole foreground
	// process group, child included; forwarding them would deliver each twice.
	fromTerminal := isTerminal(os.Stdin)
	var receivedMu sync.Mutex
	received := map[syscall.Signal]bool{}
	go func() {
		for s := range signals {
			sig := s.(syscall.Signal)
			if sig == syscall.SIGPIPE {
				continue
			}
			receivedMu.Lock()
			received[sig] = true
			receivedMu.Unlock()
			if !(fromTerminal && (sig == syscall.SIGINT || sig == syscall.SIGHUP)) {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	_ = cmd.Wait()
	done := make(chan struct{})
	go func() {
		readers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(pipeGrace):
		outR.Close()
		errR.Close()
		<-done
	}
	close(stopTick)
	stdout.end()
	stderr.end()

	status, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
	signaled := status.Signaled()
	sig := status.Signal()
	wasReceived := func() bool {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		return received[sig]
	}
	// A signal sent to the whole process group reaches us and the child
	// together; Wait can return before the signal goroutine has recorded it.
	if signaled && !wasReceived() {
		for deadline := time.Now().Add(signalGrace); time.Now().Before(deadline) && !wasReceived(); {
			time.Sleep(5 * time.Millisecond)
		}
	}
	pipelineEnded := signaled && sig == syscall.SIGPIPE && (brokenOut.Load() || brokenErr.Load())
	switch {
	case signaled && !wasReceived() && !pipelineEnded:
		client.EmitRecord(record.Record{Scope: scope, Severity: record.SeverityError, Body: "Process terminated by " + signalName(sig),
			Attrs: []record.Attr{{Key: "process.signal", Value: record.String(signalName(sig))}}})
	case !signaled && status.ExitStatus() != 0:
		code := status.ExitStatus()
		client.EmitRecord(record.Record{Scope: scope, Severity: record.SeverityError, Body: fmt.Sprintf("Process exited with code %d", code),
			Attrs: []record.Attr{{Key: "process.exit_code", Value: record.Int(int64(code))}}})
	}
	client.Flush(exitFlush)
	if signaled {
		return reraise(sig)
	}
	return status.ExitStatus()
}

// reraise ends logify run with the child's signal where the Go runtime dies
// from it after signal.Reset; for the others (SIGPIPE is ignored, SIGQUIT
// would dump goroutines) it returns 128 + n.
func reraise(sig syscall.Signal) int {
	switch sig {
	case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL:
		signal.Reset(sig)
		_ = syscall.Kill(os.Getpid(), sig)
		time.Sleep(time.Second)
	}
	return 128 + int(sig)
}
