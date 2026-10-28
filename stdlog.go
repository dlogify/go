package logify

import (
	"io"
	"log"
	"runtime"
	"strings"

	"dlogify.com/sdk/go/internal/parse"
)

// CaptureStdLog sends what the standard library's default logger writes
// (log.Print*, log.Fatal*, log.Panic*) to dlogify through the client created
// by Init, keeping its output. Shutdown undoes it. When the application
// called slog.SetDefault, the log package already goes through slog: install
// NewSlogHandler instead.
func CaptureStdLog() { Default().CaptureStdLog() }

// CaptureStdLog is CaptureStdLog for this client.
func (c *Client) CaptureStdLog() {
	if c == nil || c.exp == nil || c.closed.Load() {
		return
	}
	c.mu.Lock()
	if c.stdlog {
		c.mu.Unlock()
		return
	}
	c.stdlog = true
	c.mu.Unlock()
	prev := log.Writer()
	installed := io.MultiWriter(prev, &stdlogWriter{c: c})
	log.SetOutput(installed)
	c.addUndo(func() {
		if log.Writer() == installed {
			log.SetOutput(prev)
		}
	})
}

type stdlogWriter struct{ c *Client }

// Write gets one complete log line. log.Fatal* and log.Panic* are recognized
// from the call stack (their text says nothing), are recorded as fatal and
// flushed, since the process exits or panics right after.
func (w *stdlogWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	defer func() { _ = recover() }()
	text := strings.TrimSuffix(string(p), "\n")
	body := stripLogHeader(text, log.Prefix(), log.Flags())
	level := LevelInfo
	terminal := logTerminalCaller()
	if terminal {
		level = LevelFatal
	} else if severity, ok := parse.DetectLevel(body); ok {
		level = Level(severity)
	}
	w.c.Emit(Entry{Scope: "log", Level: level, Message: body, Skip: []string{"log", "io"}})
	if terminal {
		w.c.Flush(panicFlushTimeout)
	}
	return n, nil
}

var terminalLogFunctions = map[string]bool{
	"log.Fatal": true, "log.Fatalf": true, "log.Fatalln": true,
	"log.Panic": true, "log.Panicf": true, "log.Panicln": true,
	"log.(*Logger).Fatal": true, "log.(*Logger).Fatalf": true, "log.(*Logger).Fatalln": true,
	"log.(*Logger).Panic": true, "log.(*Logger).Panicf": true, "log.(*Logger).Panicln": true,
}

func logTerminalCaller() bool {
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	for {
		f, more := frames.Next()
		if terminalLogFunctions[f.Function] {
			return true
		}
		if !more {
			return false
		}
	}
}

// stripLogHeader removes what the log package writes before the message:
// the prefix, the date, the time and the file, in that order (the prefix
// last with Lmsgprefix).
func stripLogHeader(s, prefix string, flags int) string {
	if flags&log.Lmsgprefix == 0 {
		s = strings.TrimPrefix(s, prefix)
	}
	if flags&log.Ldate != 0 {
		s = cutField(s, len("2006/01/02 "))
	}
	if flags&(log.Ltime|log.Lmicroseconds) != 0 {
		n := len("15:04:05 ")
		if flags&log.Lmicroseconds != 0 {
			n = len("15:04:05.000000 ")
		}
		s = cutField(s, n)
	}
	if flags&(log.Lshortfile|log.Llongfile) != 0 {
		if i := strings.Index(s, ": "); i >= 0 {
			s = s[i+2:]
		}
	}
	if flags&log.Lmsgprefix != 0 {
		s = strings.TrimPrefix(s, prefix)
	}
	return s
}

func cutField(s string, n int) string {
	if len(s) >= n {
		return s[n:]
	}
	return s
}
