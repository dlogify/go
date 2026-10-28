package logify

import (
	"sync"
	"sync/atomic"
	"time"

	"dlogify.com/sdk/go/internal/record"
)

// hookRunner runs the redaction and BeforeSend on one goroutine of the
// client, in the order records arrive. A caller may hold a logging library's
// lock (the log package holds its mutex while it writes to CaptureStdLog's
// writer); running the hook there would deadlock a hook that logs through
// that library. On this goroutine, a hook that logs only produces records the
// recursion guard drops.
type hookRunner struct {
	c         *Client
	queue     chan *record.Record
	pending   atomic.Int64
	start     sync.Once
	stop      chan struct{}
	closeOnce sync.Once
}

func newHookRunner(c *Client, size int) *hookRunner {
	return &hookRunner{c: c, queue: make(chan *record.Record, size), stop: make(chan struct{})}
}

// enqueue never blocks: when the queue is full the record is dropped.
func (h *hookRunner) enqueue(rec *record.Record) {
	h.start.Do(func() { go h.loop() })
	h.pending.Add(1)
	select {
	case h.queue <- rec:
	default:
		h.pending.Add(-1)
		h.c.diag.Debug("the BeforeSend queue is full: dropped a record")
	}
}

func (h *hookRunner) loop() {
	for {
		select {
		case rec := <-h.queue:
			h.process(rec)
			h.pending.Add(-1)
		case <-h.stop:
			return
		}
	}
}

func (h *hookRunner) process(rec *record.Record) {
	defer func() {
		if r := recover(); r != nil {
			h.c.diag.Debug("dropped a record: %v", r)
		}
	}()
	if h.c.red.apply(h.c, rec) {
		record.Finalize(rec)
		h.c.exp.Enqueue(rec)
	}
}

// wait reports whether every queued record went through the hook before the
// deadline.
func (h *hookRunner) wait(deadline time.Time) bool {
	for h.pending.Load() > 0 {
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

// close stops the goroutine without waiting for a hook in progress.
func (h *hookRunner) close() {
	h.closeOnce.Do(func() { close(h.stop) })
}
