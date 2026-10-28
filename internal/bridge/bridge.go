// Package bridge connects internal/run to the root package without adding to
// the public API: the root package sets NewRunClient in its init, and
// cmd/logify imports the root package for that side effect.
package bridge

import (
	"time"

	"dlogify.com/sdk/go/internal/record"
)

type Client interface {
	// EmitRecord applies the min level, the redaction and the size cuts, and
	// queues the record (Observed is set; Time 0 means now).
	EmitRecord(r record.Record)
	Flush(timeout time.Duration) bool
	Debugf(format string, args ...any)
}

var NewRunClient func() Client
