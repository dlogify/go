package logify

import (
	"fmt"
	"net/http"
	"time"

	"dlogify.com/sdk/go/internal/record"
)

// How long Recover and the fatal levels of the adapters wait for queued
// records to be sent (docs/12 §4, guarantee 7).
const panicFlushTimeout = 2 * time.Second

// Recover records a panic, flushes for at most 2 s and panics again with the
// same value, so the program ends as it would have without dlogify. Defer it
// directly (recover only works in the deferred function itself), after
// Shutdown so it runs first:
//
//	defer logify.Shutdown(2 * time.Second)
//	defer logify.Recover()
func Recover() {
	if r := recover(); r != nil {
		Default().handlePanic(r)
	}
}

// Recover is Recover for this client: defer c.Recover().
func (c *Client) Recover() {
	if r := recover(); r != nil {
		c.handlePanic(r)
	}
}

func (c *Client) handlePanic(r any) {
	c.reportPanic(r, LevelFatal, "panic", nil)
	c.Flush(panicFlushTimeout)
	panic(r)
}

// Go runs fn in a new goroutine with Recover deferred.
func Go(fn func()) {
	go func() {
		defer Recover()
		fn()
	}()
}

// Go is Go for this client.
func (c *Client) Go(fn func()) {
	go func() {
		defer c.Recover()
		fn()
	}()
}

// Middleware records a panic in an HTTP handler at level error (the server
// keeps running) and panics again, so net/http handles it as usual.
// http.ErrAbortHandler is not recorded.
func Middleware(next http.Handler) http.Handler { return middleware(nil, next) }

// Middleware is Middleware for this client.
func (c *Client) Middleware(next http.Handler) http.Handler { return middleware(c, next) }

func middleware(c *Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v != http.ErrAbortHandler {
					client := c
					if client == nil {
						client = Default()
					}
					client.reportPanic(v, LevelError, "http", []record.Attr{
						{Key: "http.request.method", Value: record.String(r.Method)},
						{Key: "url.path", Value: record.String(r.URL.Path)},
					})
				}
				panic(v)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (c *Client) reportPanic(v any, level Level, origin string, extra []record.Attr) {
	defer func() { _ = recover() }()
	if c == nil || c.exp == nil || c.closed.Load() || !c.accepts(int(level)) {
		return
	}
	typ, msg := "panic", fmt.Sprint(v)
	if err, ok := v.(error); ok {
		typ, msg = fmt.Sprintf("%T", err), safeError(err)
	}
	now := time.Now().UnixNano()
	rec := record.Record{
		Scope: "uncaught", Severity: int(level), Body: "panic: " + msg, Time: now, Observed: now,
		Attrs: []record.Attr{
			{Key: "exception.type", Value: record.String(typ)},
			{Key: "exception.message", Value: record.String(msg)},
			{Key: "exception.stacktrace", Value: record.String("panic: " + msg + "\n\n" + panicStack())},
			{Key: "exception.origin", Value: record.String(origin)},
		},
	}
	rec.Attrs = append(rec.Attrs, extra...)
	c.emit(&rec)
}
