package logify_test

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	logify "dlogify.com/sdk/go"
)

func TestLogSendsATypedRecord(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{Service: "checkout", Environment: "test", Release: "1.0.0", Attributes: map[string]any{"team": "pay"}})
	c.Warn("disk almost full",
		slog.Int("pct", 91), slog.Float64("ratio", 0.91), slog.Bool("ok", false), slog.String("disk", "/"),
		slog.Group("req", slog.String("id", "r1")), slog.Duration("took", time.Second))
	got := flushed(t, c, s)
	if len(got) != 1 {
		t.Fatalf("got %d records", len(got))
	}
	r := got[0]
	if r.Scope != "logify" || r.Severity != 13 || r.Text != "WARN" || r.Body != "disk almost full" {
		t.Fatalf("got %+v", r)
	}
	want := map[string]any{"pct": int64(91), "ratio": 0.91, "ok": false, "disk": "/", "req.id": "r1", "took": int64(time.Second)}
	for k, v := range want {
		if r.Attrs[k] != v {
			t.Fatalf("attribute %s: got %#v, want %#v", k, r.Attrs[k], v)
		}
	}
	res := s.resource
	if res["service.name"] != "checkout" || res["deployment.environment.name"] != "test" || res["service.version"] != "1.0.0" ||
		res["telemetry.sdk.name"] != "logify.go" || res["telemetry.sdk.language"] != "go" || res["team"] != "pay" {
		t.Fatalf("resource %v", res)
	}
}

func TestMinLevel(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{MinLevel: logify.LevelWarn})
	c.Info("no")
	c.Error("yes")
	flushed(t, c, s)
	if got := strings.Join(s.bodies(), ","); got != "yes" {
		t.Fatalf("got %s", got)
	}
	if c.Enabled(logify.LevelInfo) || !c.Enabled(logify.LevelError) {
		t.Fatal("Enabled")
	}
}

func TestErrorBecomesException(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	err := fmt.Errorf("charge: %w", errors.New("connection refused"))
	c.Error("charge failed", slog.Any("err", err), slog.String("order", "o1"))
	r := flushed(t, c, s)[0]
	if r.Attrs["exception.type"] != "*fmt.wrapError" || r.Attrs["exception.message"] != "charge: connection refused" || r.Attrs["order"] != "o1" {
		t.Fatalf("attributes %v", r.Attrs)
	}
	if _, ok := r.Attrs["err"]; ok {
		t.Fatal("the error must not stay an attribute")
	}
	trace := r.Attrs["exception.stacktrace"].(string)
	if !strings.HasPrefix(trace, "goroutine ") ||
		!strings.Contains(trace, "\ndlogify.com/sdk/go_test.TestErrorBecomesException(...)\n\t") ||
		!strings.Contains(trace, "client_test.go:") ||
		strings.Contains(trace, "dlogify.com/sdk/go.(*Client)") ||
		!strings.HasSuffix(trace, "\nCaused by: *errors.errorString: connection refused") {
		t.Fatalf("stack trace:\n%s", trace)
	}
	if !regexp.MustCompile(`(?m)^\t\S.*client_test\.go:\d+ \+0x[0-9a-f]+$`).MatchString(trace) {
		t.Fatalf("frame lines do not match the engine's Go format:\n%s", trace)
	}
}

func TestJoinedErrorsAreCauses(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	c.CaptureError(errors.Join(errors.New("a"), errors.New("b")))
	r := flushed(t, c, s)[0]
	trace := r.Attrs["exception.stacktrace"].(string)
	if r.Body != "*errors.joinError: a\nb" || r.Severity != 17 ||
		!strings.Contains(trace, "\nCaused by: *errors.errorString: a\nCaused by: *errors.errorString: b") {
		t.Fatalf("got %+v", r)
	}
}

func TestCaptureErrorIgnoresNil(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	c.CaptureError(nil)
	if len(flushed(t, c, s)) != 0 {
		t.Fatal("nil error sent")
	}
}

type panickyError struct{}

func (panickyError) Error() string { panic("Error() panics") }

func TestHostileValuesNeverPanic(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	var nilErr *panickyErrorPtr
	c.Error("hostile", slog.Any("bad", panickyError{}), slog.Any("ch", make(chan int)), slog.Any("nilptr", (*int)(nil)), slog.Any("nilerr", nilErr))
	r := flushed(t, c, s)[0]
	if r.Attrs["exception.type"] != "logify_test.panickyError" || r.Attrs["exception.message"] != "logify_test.panickyError" {
		t.Fatalf("attributes %v", r.Attrs)
	}
}

type panickyErrorPtr struct{}

func (*panickyErrorPtr) Error() string { panic("nil receiver") }

func TestDisabledClientDoesNothing(t *testing.T) {
	c := logify.New(logify.Options{})
	c.Error("x")
	if !c.Flush(time.Millisecond) || c.Enabled(logify.LevelFatal) {
		t.Fatal("disabled client")
	}
	c.Shutdown(time.Millisecond)
	c.Shutdown(time.Millisecond)
}

func TestCallsAfterShutdownAreDropped(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	c.Info("before")
	c.Shutdown(2 * time.Second)
	c.Info("after")
	time.Sleep(50 * time.Millisecond)
	if got := strings.Join(s.bodies(), ","); got != "before" {
		t.Fatalf("got %s", got)
	}
}

func TestConcurrentLogAndShutdown(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Info("x", slog.Int("j", j))
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	c.Shutdown(time.Second)
	wg.Wait()
}

func TestBeforeSend(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{BeforeSend: func(e *logify.Event) *logify.Event {
		switch e.Message {
		case "drop":
			return nil
		case "panic":
			panic("boom")
		}
		e.Message = strings.ToUpper(e.Message)
		e.Attributes["added"] = 1
		delete(e.Attributes, "secret")
		e.Level = logify.LevelFatal // ignored
		return e
	}})
	c.Info("drop")
	c.Info("panic")
	c.Info("keep", slog.String("secret", "x"), slog.String("a", "b"))
	got := flushed(t, c, s)
	if len(got) != 1 || got[0].Body != "KEEP" || got[0].Severity != 9 || len(got[0].Attrs) != 2 ||
		got[0].Attrs["a"] != "b" || got[0].Attrs["added"] != int64(1) {
		t.Fatalf("got %+v", got)
	}
}

func TestBeforeSendRecursionIsDropped(t *testing.T) {
	s := newIngest(t)
	var c *logify.Client
	c = newClient(t, s, logify.Options{BeforeSend: func(e *logify.Event) *logify.Event {
		c.Error("from inside the hook")
		return e
	}})
	done := make(chan struct{})
	go func() {
		c.Info("outer")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock")
	}
	flushed(t, c, s)
	if got := strings.Join(s.bodies(), ","); got != "outer" {
		t.Fatalf("got %s", got)
	}
}

func TestInvalidRedactionEntriesAreIgnored(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{
		RedactKeyPatterns: []*regexp.Regexp{nil, regexp.MustCompile(`^x-`)},
		RedactPatterns:    []logify.RedactPattern{{}, {Pattern: regexp.MustCompile(`\d{4}`)}},
	})
	c.Info("pin 1234", slog.String("x-ref", "v"), slog.Int("x-n", 7))
	r := flushed(t, c, s)[0]
	if r.Body != "pin <redacted>" || r.Attrs["x-ref"] != "<redacted>" || r.Attrs["x-n"] != "<redacted>" {
		t.Fatalf("got %+v", r)
	}
}
