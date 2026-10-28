// Package diag writes the client's own messages straight to os.Stderr, never
// through log or slog, so a capture can never see them (docs/12 §4,
// guarantee 3).
package diag

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

var (
	outMu sync.Mutex
	out   io.Writer = os.Stderr
)

// SetOutput replaces the destination (tests) and returns a restore function.
func SetOutput(w io.Writer) (restore func()) {
	outMu.Lock()
	prev := out
	out = w
	outMu.Unlock()
	return func() {
		outMu.Lock()
		out = prev
		outMu.Unlock()
	}
}

func write(text string) {
	outMu.Lock()
	defer outMu.Unlock()
	_, _ = io.WriteString(out, text) // stderr closed: nowhere left to report to
}

type Diag struct {
	debug bool
	mu    sync.Mutex
	seen  map[string]bool
}

func New(debug bool) *Diag { return &Diag{debug: debug, seen: map[string]bool{}} }

func (d *Diag) DebugEnabled() bool { return d != nil && d.debug }

func (d *Diag) Warn(format string, args ...any) {
	if d == nil {
		return
	}
	write("logify: warning: " + fmt.Sprintf(format, args...) + "\n")
}

func (d *Diag) WarnOnce(key, format string, args ...any) {
	if d.first(key) {
		d.Warn(format, args...)
	}
}

func (d *Diag) Debug(format string, args ...any) {
	if !d.DebugEnabled() {
		return
	}
	write("logify: " + fmt.Sprintf(format, args...) + "\n")
}

func (d *Diag) DebugOnce(key, format string, args ...any) {
	if d.DebugEnabled() && d.first("debug:"+key) {
		d.Debug(format, args...)
	}
}

func (d *Diag) first(key string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen[key] {
		return false
	}
	d.seen[key] = true
	return true
}

func IsFlag(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true"
}
