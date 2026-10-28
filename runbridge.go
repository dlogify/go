package logify

import (
	"os"
	"time"

	"dlogify.com/sdk/go/internal/bridge"
	"dlogify.com/sdk/go/internal/record"
	"dlogify.com/sdk/go/internal/version"
)

// logify run is not the application: a client from the environment only,
// with " run" appended to the User-Agent (docs/12 §2).
func init() {
	bridge.NewRunClient = func() bridge.Client {
		return runClient{newClient(Options{}, version.UserAgent+" run", os.Getenv)}
	}
}

type runClient struct{ c *Client }

func (r runClient) EmitRecord(rec record.Record) {
	c := r.c
	if c.exp == nil || c.closed.Load() || !c.accepts(rec.Severity) {
		return
	}
	defer func() { _ = recover() }()
	rec.Observed = time.Now().UnixNano()
	if rec.Time == 0 {
		rec.Time = rec.Observed
	}
	c.emit(&rec)
}

func (r runClient) Flush(timeout time.Duration) bool { return r.c.Flush(timeout) }

func (r runClient) Debugf(format string, args ...any) { r.c.diag.Debug(format, args...) }
