package logify

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"dlogify.com/sdk/go/internal/diag"
	"dlogify.com/sdk/go/internal/encode"
	"dlogify.com/sdk/go/internal/export"
	"dlogify.com/sdk/go/internal/goid"
	"dlogify.com/sdk/go/internal/record"
	"dlogify.com/sdk/go/internal/version"
)

// Entry is a record handed over by a logging library adapter
// (dlogify.com/sdk/go/zap, …/zerolog, …/logrus). Applications use the Log
// methods instead.
type Entry struct {
	// Scope names the source, for example "zap"; default "logify".
	Scope string
	// Level; the zero value means info.
	Level Level
	// Time of the call; the zero value means now.
	Time    time.Time
	Message string
	Attrs   []slog.Attr
	// Err becomes exception.*; when nil, the first error value in Attrs does.
	Err error
	// ErrText is used when the library only has the error's text: it becomes
	// exception.message, with no exception.type.
	ErrText string
	// Skip lists package path prefixes of the logging library, removed from
	// the top of the captured stack (for example "go.uber.org/zap").
	Skip []string
}

// Client sends records to dlogify. The zero value is not usable; create one
// with New, or use the package functions after Init.
type Client struct {
	cfg   config
	diag  *diag.Diag
	exp   *export.Exporter
	red   *redactor
	hooks *hookRunner // set when BeforeSend is
	noop  bool

	closed       atomic.Bool
	shutdownOnce sync.Once
	mu           sync.Mutex
	undo         []func()
	stdlog       bool     // CaptureStdLog installed
	inHook       sync.Map // goroutine ids inside BeforeSend
	hookDrops    atomic.Int64
	hookFailures atomic.Int64
}

// New creates an independent client from opts and the environment. It never
// fails: with a broken configuration the client is a no-op and says why on
// stderr.
func New(opts Options) *Client {
	return newClient(opts, version.UserAgent, os.Getenv)
}

func newClient(opts Options, userAgent string, getenv func(string) string) (c *Client) {
	c = &Client{diag: diag.New(false)}
	defer func() {
		if r := recover(); r != nil {
			c.exp = nil
			c.diag.Warn("could not start (%v); Logify is disabled", r)
		}
	}()
	cfg, warnings := resolveConfig(opts, getenv)
	c.cfg = cfg
	c.diag = diag.New(cfg.debug)
	red, more := newRedactor(cfg, opts)
	c.red = red
	for _, w := range append(warnings, more...) {
		c.diag.Warn("%s", w)
	}
	if !cfg.enabled {
		return c
	}
	if red != nil && red.before != nil {
		c.hooks = newHookRunner(c, cfg.maxQueueSize)
	}
	c.exp = export.New(export.Options{
		URL:       cfg.logsURL,
		APIKey:    cfg.apiKey,
		UserAgent: userAgent,
		Resource: encode.Resource(encode.ResourceConfig{
			Service: cfg.service, Environment: cfg.environment, Release: cfg.release, Attributes: cfg.attributes,
		}),
		MaxQueueSize: cfg.maxQueueSize,
		Diag:         c.diag,
	})
	return c
}

func severityOf(l Level) int {
	if l.valid() {
		return int(l)
	}
	return record.SeverityInfo
}

// Unknown levels (only logify run produces them) rank as info.
func (c *Client) accepts(severity int) bool {
	if severity == record.SeverityUnknown {
		severity = record.SeverityInfo
	}
	return severity >= int(c.cfg.minLevel)
}

// Enabled reports whether a record at this level would be sent.
func (c *Client) Enabled(level Level) bool {
	return c != nil && c.exp != nil && !c.closed.Load() && c.accepts(severityOf(level))
}

func (c *Client) Trace(msg string, attrs ...slog.Attr) { c.Log(LevelTrace, msg, attrs...) }
func (c *Client) Debug(msg string, attrs ...slog.Attr) { c.Log(LevelDebug, msg, attrs...) }
func (c *Client) Info(msg string, attrs ...slog.Attr)  { c.Log(LevelInfo, msg, attrs...) }
func (c *Client) Warn(msg string, attrs ...slog.Attr)  { c.Log(LevelWarn, msg, attrs...) }
func (c *Client) Error(msg string, attrs ...slog.Attr) { c.Log(LevelError, msg, attrs...) }

// Fatal records at level fatal; it does not end the process.
func (c *Client) Fatal(msg string, attrs ...slog.Attr) { c.Log(LevelFatal, msg, attrs...) }

// Log records msg at level (an unknown level logs at info). The first
// attribute whose value is an error becomes the record's exception, with the
// stack of this call.
func (c *Client) Log(level Level, msg string, attrs ...slog.Attr) {
	c.Emit(Entry{Scope: "logify", Level: level, Message: msg, Attrs: attrs})
}

// CaptureError records err at level error with the message "<type>: <message>".
func (c *Client) CaptureError(err error, attrs ...slog.Attr) {
	if err == nil {
		return
	}
	c.Emit(Entry{Scope: "logify", Level: LevelError, Err: err, Attrs: attrs})
}

// Emit records an entry from a logging library adapter.
func (c *Client) Emit(e Entry) {
	if c == nil {
		return
	}
	if c.exp == nil {
		if c.noop {
			c.diag.DebugOnce("before-init", "a record was logged before logify.Init; it was dropped")
		}
		return
	}
	if c.closed.Load() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			c.diag.Debug("dropped a record: %v", r)
		}
	}()
	severity := severityOf(e.Level)
	if !c.accepts(severity) {
		return
	}
	now := time.Now().UnixNano()
	rec := record.Record{Scope: e.Scope, Severity: severity, Body: e.Message, Time: now, Observed: now}
	if rec.Scope == "" {
		rec.Scope = "logify"
	}
	if !e.Time.IsZero() {
		rec.Time = e.Time.UnixNano()
	}
	attrs, attrErr := flattenAttrs(e.Attrs)
	err := e.Err
	if err == nil {
		err = attrErr
	}
	if err != nil || e.ErrText != "" {
		rec.Attrs = exceptionAttrs(err, e.ErrText, callerStack(e.Skip))
		if rec.Body == "" {
			rec.Body = exceptionTitle(err, e.ErrText)
		}
	}
	for _, a := range attrs {
		rec.Attrs = record.Set(rec.Attrs, a.Key, a.Value)
	}
	c.emit(&rec)
}

// emit runs the redaction, the size cuts and queues the record.
func (c *Client) emit(rec *record.Record) {
	if c.red != nil {
		if c.red.before != nil {
			if _, inside := c.inHook.Load(goid.Get()); inside {
				c.diag.Debug("dropped a record logged inside BeforeSend")
				return
			}
			c.hooks.enqueue(rec)
			return
		}
		if !c.red.apply(c, rec) {
			return
		}
	}
	record.Finalize(rec)
	c.exp.Enqueue(rec)
}

// Flush sends everything queued; it returns true when done before the timeout.
func (c *Client) Flush(timeout time.Duration) bool {
	if c == nil || c.exp == nil {
		return true
	}
	if timeout < 0 {
		timeout = 0
	}
	deadline := time.Now().Add(timeout)
	if c.hooks != nil && !c.hooks.wait(deadline) {
		return false
	}
	return c.exp.Flush(max(time.Until(deadline), 0))
}

// Shutdown flushes for at most timeout, stops the client and undoes what it
// installed (CaptureStdLog). Later calls do nothing.
func (c *Client) Shutdown(timeout time.Duration) {
	if c == nil {
		return
	}
	c.shutdownOnce.Do(func() {
		c.Flush(timeout)
		c.closed.Store(true)
		if c.hooks != nil {
			c.hooks.close()
		}
		if c.exp != nil {
			c.exp.Close()
		}
		c.mu.Lock()
		undo := c.undo
		c.undo = nil
		c.mu.Unlock()
		for i := len(undo) - 1; i >= 0; i-- {
			func() {
				defer func() { _ = recover() }()
				undo[i]()
			}()
		}
	})
}

func (c *Client) addUndo(f func()) {
	c.mu.Lock()
	c.undo = append(c.undo, f)
	c.mu.Unlock()
}
