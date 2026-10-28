package logifyzerolog_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"dlogify.com/sdk/go/internal/ingesttest"
	logifyzerolog "dlogify.com/sdk/go/zerolog"
)

func TestZerologWriter(t *testing.T) {
	s := ingesttest.New(t)
	c := s.Client(t)
	logger := zerolog.New(zerolog.MultiLevelWriter(io.Discard, logifyzerolog.Writer(logifyzerolog.WithClient(c)))).
		With().Timestamp().Str("svc", "a").Logger()
	logger.Error().Err(errors.New("declined")).Int("n", 2).Dict("req", zerolog.Dict().Str("id", "r1")).Msg("charge failed")
	logger.Debug().Msg("hidden")
	func() {
		defer func() { _ = recover() }()
		logger.Panic().Msg("bad state")
	}()
	c.Flush(2e9)
	got := s.Records()
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	r := got[0]
	trace, _ := r.Attrs["exception.stacktrace"].(string)
	if r.Scope != "zerolog" || r.Severity != 17 || r.Body != "charge failed" || r.Attrs["svc"] != "a" || r.Attrs["n"] != int64(2) ||
		r.Attrs["req"] != `{"id":"r1"}` || r.Attrs["exception.message"] != "declined" || r.Attrs["exception.type"] != nil ||
		r.Attrs["error"] != nil || r.Attrs["level"] != nil || r.Attrs["time"] != nil ||
		!strings.Contains(trace, "dlogify.com/sdk/go/zerolog_test.TestZerologWriter(...)") || strings.Contains(trace, "github.com/rs/zerolog.") {
		t.Fatalf("got %+v\n%s", r, trace)
	}
	if got[1].Severity != 21 || got[1].Body != "bad state" {
		t.Fatalf("panic record %+v", got[1])
	}
}
