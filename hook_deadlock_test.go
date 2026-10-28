package logify_test

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	logify "dlogify.com/sdk/go"
)

// A BeforeSend that logs through the log package, or through slog's default
// handler (which writes through log), while CaptureStdLog is installed: the
// outer log call must return and the hook's own record must be dropped. Each
// variant runs in a child process, so a deadlock cannot hang this binary.
func TestBeforeSendLoggingThroughLogDoesNotDeadlock(t *testing.T) {
	if variant := os.Getenv("LOGIFY_TEST_HOOK_VARIANT"); variant != "" {
		c := logify.New(logify.Options{APIKey: "lgf_test", Endpoint: os.Getenv("LOGIFY_TEST_ENDPOINT"),
			BeforeSend: func(e *logify.Event) *logify.Event {
				if variant == "log" {
					log.Print("from the hook")
				} else {
					slog.Info("from the hook")
				}
				return e
			}})
		c.CaptureStdLog()
		log.Print("ERROR: outer")
		c.Flush(2 * time.Second)
		os.Exit(0)
	}
	for _, variant := range []string{"log", "slog"} {
		s := newIngest(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBeforeSendLoggingThroughLogDoesNotDeadlock$")
		cmd.Env = append(os.Environ(), "LOGIFY_TEST_HOOK_VARIANT="+variant, "LOGIFY_TEST_ENDPOINT="+s.URL)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s: %v (a timeout means a deadlock)\n%s", variant, err, out)
		}
		if got := strings.Join(s.bodies(), "|"); got != "ERROR: outer" {
			t.Fatalf("%s: sent %q", variant, got)
		}
	}
}
