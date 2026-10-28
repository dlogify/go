package logify

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"dlogify.com/sdk/go/internal/diag"
)

var (
	std    atomic.Pointer[Client]
	initMu sync.Mutex
	noop   = sync.OnceValue(func() *Client {
		return &Client{diag: diag.New(diag.IsFlag(os.Getenv("LOGIFY_DEBUG"))), noop: true}
	})
)

// Init creates the client the package functions use. A second call is
// ignored. Call it early in main, then defer Shutdown.
func Init(opts Options) {
	initMu.Lock()
	defer initMu.Unlock()
	if c := std.Load(); c != nil {
		c.diag.Debug("logify.Init was already called; this call is ignored")
		return
	}
	std.Store(New(opts))
}

// Default returns the client created by Init, or a client that drops
// everything when Init has not been called.
func Default() *Client {
	if c := std.Load(); c != nil {
		return c
	}
	return noop()
}

func Trace(msg string, attrs ...slog.Attr) { Default().Log(LevelTrace, msg, attrs...) }
func Debug(msg string, attrs ...slog.Attr) { Default().Log(LevelDebug, msg, attrs...) }
func Info(msg string, attrs ...slog.Attr)  { Default().Log(LevelInfo, msg, attrs...) }
func Warn(msg string, attrs ...slog.Attr)  { Default().Log(LevelWarn, msg, attrs...) }
func Error(msg string, attrs ...slog.Attr) { Default().Log(LevelError, msg, attrs...) }

// Fatal records at level fatal; it does not end the process.
func Fatal(msg string, attrs ...slog.Attr) { Default().Log(LevelFatal, msg, attrs...) }

func Log(level Level, msg string, attrs ...slog.Attr) { Default().Log(level, msg, attrs...) }

func CaptureError(err error, attrs ...slog.Attr) { Default().CaptureError(err, attrs...) }

// Flush sends everything queued; it returns true when done before the timeout.
func Flush(timeout time.Duration) bool { return Default().Flush(timeout) }

// Shutdown flushes for at most timeout and stops the client. Go has no exit
// hook: defer it in main.
func Shutdown(timeout time.Duration) { Default().Shutdown(timeout) }
