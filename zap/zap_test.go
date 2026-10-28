package logifyzap_test

import (
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"dlogify.com/sdk/go/internal/ingesttest"
	logifyzap "dlogify.com/sdk/go/zap"
)

func TestZapCore(t *testing.T) {
	s := ingesttest.New(t)
	c := s.Client(t)
	logger := zap.New(zapcore.NewTee(zapcore.NewNopCore(), logifyzap.NewCore(logifyzap.WithClient(c)))).Named("api").With(zap.String("svc", "a"))
	logger.Error("charge failed", zap.Error(errors.New("declined")), zap.Int("n", 2))
	logger.Debug("hidden")
	logger.DPanic("dpanic in production does not panic")
	func() {
		defer func() { _ = recover() }()
		logger.Panic("bad state")
	}()
	c.Flush(2e9)
	got := s.Records()
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	r := got[0]
	trace, _ := r.Attrs["exception.stacktrace"].(string)
	if r.Scope != "zap" || r.Severity != 17 || r.Body != "charge failed" || r.Attrs["logger.name"] != "api" ||
		r.Attrs["svc"] != "a" || r.Attrs["n"] != int64(2) || r.Attrs["exception.type"] != "*errors.errorString" ||
		r.Attrs["exception.message"] != "declined" || r.Attrs["error"] != nil ||
		!strings.Contains(trace, "dlogify.com/sdk/go/zap_test.TestZapCore(...)") || strings.Contains(trace, "go.uber.org/zap") {
		t.Fatalf("got %+v\n%s", r, trace)
	}
	if got[1].Severity != 17 || got[2].Severity != 21 {
		t.Fatalf("levels %d %d", got[1].Severity, got[2].Severity)
	}
}
