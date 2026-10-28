package logify_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	logify "dlogify.com/sdk/go"
)

// The only test that calls Init: the singleton lives for the whole test
// binary, so a repeated run (-count=N) has nothing left to check.
var singletonTested bool

func TestCallsBeforeInitAndAfterShutdown(t *testing.T) {
	if singletonTested {
		t.Skip("logify.Init works once per process")
	}
	singletonTested = true
	logify.Info("before init")
	s := newIngest(t)
	logify.Init(logify.Options{APIKey: "lgf_test", Endpoint: s.URL})
	logify.Init(logify.Options{APIKey: "other", Endpoint: "http://127.0.0.1:1"})
	logify.Error("after init", slog.String("k", "v"))
	logify.CaptureError(errors.New("boom"))
	logify.Log(logify.LevelWarn, "warned")
	if !logify.Flush(2 * time.Second) {
		t.Fatal("flush timed out")
	}
	if got := strings.Join(s.bodies(), "|"); got != "after init|*errors.errorString: boom|warned" {
		t.Fatalf("got %s", got)
	}
	logify.Shutdown(time.Second)
	logify.Error("after shutdown")
	time.Sleep(50 * time.Millisecond)
	if len(s.all()) != 3 {
		t.Fatal("record after Shutdown was sent")
	}
}
