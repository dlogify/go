package logify_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"

	logify "dlogify.com/sdk/go"
)

func TestSlogHandlerTeesAndSends(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	var buf bytes.Buffer
	logger := slog.New(c.NewSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger = logger.With("svc", "a").WithGroup("req").With("id", "r1")
	logger.Error("boom", "err", errors.New("x"), "n", 2)
	logger.Debug("dbg")
	if !strings.Contains(buf.String(), "msg=boom") || !strings.Contains(buf.String(), "msg=dbg") {
		t.Fatalf("next handler output: %s", buf.String())
	}
	got := flushed(t, c, s)
	if len(got) != 1 {
		t.Fatalf("got %d records (debug must stay below the min level)", len(got))
	}
	r := got[0]
	if r.Scope != "slog" || r.Body != "boom" || r.Severity != 17 || r.Attrs["svc"] != "a" || r.Attrs["req.id"] != "r1" ||
		r.Attrs["req.n"] != int64(2) || r.Attrs["exception.type"] != "*errors.errorString" {
		t.Fatalf("got %+v", r)
	}
	trace := r.Attrs["exception.stacktrace"].(string)
	if !strings.Contains(trace, "dlogify.com/sdk/go_test.TestSlogHandlerTeesAndSends(...)") || strings.Contains(trace, "log/slog.") {
		t.Fatalf("stack:\n%s", trace)
	}
}

func TestSlogLevels(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{MinLevel: logify.LevelTrace})
	logger := slog.New(c.NewSlogHandler(nil))
	ctx := context.Background()
	for _, l := range []slog.Level{slog.LevelDebug - 4, slog.LevelDebug, slog.LevelInfo + 2, slog.LevelWarn, slog.LevelError, slog.LevelError + 4} {
		logger.Log(ctx, l, l.String())
	}
	var got []int
	for _, r := range flushed(t, c, s) {
		got = append(got, r.Severity)
	}
	want := []int{1, 5, 9, 13, 17, 21}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("severities %v, want %v", got, want)
		}
	}
}

func TestSlogHandlerEnabled(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{MinLevel: logify.LevelWarn})
	h := c.NewSlogHandler(nil)
	if h.Enabled(context.Background(), slog.LevelInfo) || !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("Enabled without next")
	}
	h = c.NewSlogHandler(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if !h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("Enabled must follow next too")
	}
}

func TestSlogSetDefaultCarriesTheLogPackage(t *testing.T) {
	prevLogger, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()
	defer func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	slog.SetDefault(slog.New(c.NewSlogHandler(nil)))
	log.Print("from the log package")
	got := flushed(t, c, s)
	if len(got) != 1 || got[0].Body != "from the log package" || got[0].Scope != "slog" {
		t.Fatalf("got %+v", got)
	}
}
