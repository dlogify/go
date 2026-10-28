package logifylogrus_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"dlogify.com/sdk/go/internal/ingesttest"
	logifylogrus "dlogify.com/sdk/go/logrus"
)

func TestLogrusHook(t *testing.T) {
	s := ingesttest.New(t)
	c := s.Client(t)
	logger := logrus.New()
	logger.Out = io.Discard
	logger.AddHook(logifylogrus.NewHook(logifylogrus.WithClient(c)))
	logger.WithField("svc", "a").WithField("n", 2).WithError(errors.New("declined")).Error("charge failed")
	logger.Debug("hidden")
	func() {
		defer func() { _ = recover() }()
		logger.Panic("bad state")
	}()
	c.Flush(2e9)
	got := s.Records()
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	r := got[0]
	trace, _ := r.Attrs["exception.stacktrace"].(string)
	if r.Scope != "logrus" || r.Severity != 17 || r.Body != "charge failed" || r.Attrs["svc"] != "a" || r.Attrs["n"] != int64(2) ||
		r.Attrs["exception.type"] != "*errors.errorString" || r.Attrs["exception.message"] != "declined" || r.Attrs["error"] != nil ||
		!strings.Contains(trace, "dlogify.com/sdk/go/logrus_test.TestLogrusHook(...)") || strings.Contains(trace, "github.com/sirupsen/logrus.") {
		t.Fatalf("got %+v\n%s", r, trace)
	}
	if got[1].Severity != 21 {
		t.Fatalf("panic record %+v", got[1])
	}
}
