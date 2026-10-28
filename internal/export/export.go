// Package export queues encoded records and sends them in the background
// (docs/12 §4): one goroutine per exporter, one request in flight, retries
// with backoff, and failures that never reach the application.
package export

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"dlogify.com/sdk/go/internal/diag"
	"dlogify.com/sdk/go/internal/encode"
	"dlogify.com/sdk/go/internal/record"
)

const (
	DefaultInterval = 5 * time.Second
	TriggerRecords  = 500
	TriggerBytes    = 512 * 1024
	BatchMaxRecords = 1000
	BatchMaxBytes   = 1_000_000
	RequestTimeout  = 10 * time.Second
	RetryWindow     = 5 * time.Minute

	backoffBase = time.Second
	backoffCap  = 30 * time.Second
	// Repeated "Retry-After: 0" replies must not become a tight loop.
	retryAfterFloor = 100 * time.Millisecond
	// Room for the scope wrappers around the records of one request.
	envelopeReserve = 1024
)

type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type Options struct {
	URL          string
	APIKey       string
	UserAgent    string
	Resource     []record.Attr
	MaxQueueSize int
	Diag         *diag.Diag
	HTTPClient   *http.Client
	Clock        Clock
	Rand         func() float64
	Interval     time.Duration
}

type Exporter struct {
	o           Options
	batchBudget int
	ctx         context.Context
	cancel      context.CancelFunc
	wake        chan struct{}
	done        chan struct{}

	mu              sync.Mutex
	ring            []encode.Encoded
	head, n         int
	bytes           int
	dropped         int
	reportedDropped int
	inFlight        bool
	stopped         bool
	closed          bool
	started         bool
	idle            chan struct{} // closed while there is nothing to send
	idleClosed      bool
}

func New(o Options) *Exporter {
	if o.MaxQueueSize <= 0 {
		o.MaxQueueSize = 2000
	}
	if o.HTTPClient == nil {
		o.HTTPClient = defaultHTTPClient()
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	e := &Exporter{
		o:    o,
		ring: make([]encode.Encoded, o.MaxQueueSize),
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
		idle: make(chan struct{}),
	}
	close(e.idle)
	e.idleClosed = true
	e.batchBudget = BatchMaxBytes - len(encode.Request(o.Resource, nil)) - envelopeReserve
	e.ctx, e.cancel = context.WithCancel(context.Background())
	return e
}

// Enqueue encodes the record in the caller's goroutine and queues it. It
// never blocks on the network; when the queue is full the oldest record goes.
func (e *Exporter) Enqueue(r *record.Record) {
	enc := encode.Record(r)
	e.mu.Lock()
	if e.stopped || e.closed {
		e.mu.Unlock()
		return
	}
	size := len(e.ring)
	if e.n == size {
		e.bytes -= len(e.ring[e.head].JSON)
		e.ring[e.head] = encode.Encoded{}
		e.head = (e.head + 1) % size
		e.n--
		e.dropped++
	}
	e.ring[(e.head+e.n)%size] = enc
	e.n++
	e.bytes += len(enc.JSON)
	if e.idleClosed {
		e.idle = make(chan struct{})
		e.idleClosed = false
	}
	trigger := e.n >= TriggerRecords || e.bytes >= TriggerBytes
	start := !e.started
	e.started = true
	e.mu.Unlock()
	if start {
		go e.loop()
	}
	if trigger {
		e.kick()
	}
}

// ensureLoop starts the export goroutine if it is not running (tests hold it
// back by setting started while they fill the queue).
func (e *Exporter) ensureLoop() {
	e.mu.Lock()
	start := !e.started && !e.closed
	e.started = true
	e.mu.Unlock()
	if start {
		go e.loop()
	}
}

func (e *Exporter) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Flush sends everything queued; true when done before the timeout.
func (e *Exporter) Flush(timeout time.Duration) bool {
	e.mu.Lock()
	if e.idleClosed {
		e.mu.Unlock()
		return true
	}
	ch := e.idle
	e.mu.Unlock()
	e.kick()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

// Close drops what is queued, aborts a request or a retry sleep in progress
// and waits for the export goroutine to end. Later records are ignored.
func (e *Exporter) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.clearLocked()
	started := e.started
	e.mu.Unlock()
	e.cancel()
	if started {
		<-e.done
	}
	e.mu.Lock()
	e.setIdleLocked()
	e.mu.Unlock()
}

func (e *Exporter) clearLocked() {
	for i := range e.ring {
		e.ring[i] = encode.Encoded{}
	}
	e.head, e.n, e.bytes = 0, 0, 0
}

func (e *Exporter) setIdleLocked() {
	if e.n == 0 && !e.inFlight && !e.idleClosed {
		close(e.idle)
		e.idleClosed = true
	}
}

func (e *Exporter) loop() {
	defer close(e.done)
	ticker := time.NewTicker(e.o.Interval)
	defer ticker.Stop()
	for {
		e.drain()
		select {
		case <-e.ctx.Done():
			return
		case <-e.wake:
		case <-ticker.C:
		}
	}
}

func (e *Exporter) drain() {
	for {
		e.mu.Lock()
		if e.n == 0 || e.stopped || e.closed {
			e.setIdleLocked()
			e.mu.Unlock()
			e.reportDropped()
			return
		}
		batch := e.takeBatchLocked()
		e.inFlight = true
		e.mu.Unlock()
		e.reportDropped()
		e.send(batch)
		e.mu.Lock()
		e.inFlight = false
		e.mu.Unlock()
	}
}

func (e *Exporter) takeBatchLocked() []encode.Encoded {
	size := len(e.ring)
	var batch []encode.Encoded
	bytes := 0
	for len(batch) < e.n && len(batch) < BatchMaxRecords {
		item := e.ring[(e.head+len(batch))%size]
		cost := len(item.JSON) + 1
		if len(batch) > 0 && bytes+cost > e.batchBudget {
			break
		}
		bytes += cost
		batch = append(batch, item)
	}
	for range batch {
		e.bytes -= len(e.ring[e.head].JSON)
		e.ring[e.head] = encode.Encoded{}
		e.head = (e.head + 1) % size
		e.n--
	}
	return batch
}

func (e *Exporter) reportDropped() {
	e.mu.Lock()
	fresh := e.dropped - e.reportedDropped
	total := e.dropped
	e.reportedDropped = e.dropped
	e.mu.Unlock()
	if fresh > 0 {
		e.o.Diag.Debug("queue full: dropped %d records (%d in total)", fresh, total)
	}
}

func (e *Exporter) finished() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopped || e.closed
}

func (e *Exporter) send(batch []encode.Encoded) {
	started := e.o.Clock.Now()
	for attempt := 0; !e.finished(); attempt++ {
		out := e.post(batch)
		switch out.kind {
		case sent:
			e.reportPartial(out.body)
			return
		case stop:
			e.stop(out.status)
			return
		case drop:
			e.o.Diag.Debug("dropped %d records: %s", len(batch), out.reason)
			return
		case split:
			if len(batch) == 1 {
				e.o.Diag.Debug("dropped a record that is too large (HTTP 413)")
				return
			}
			half := (len(batch) + 1) / 2
			e.send(batch[:half])
			e.send(batch[half:])
			return
		case retry:
			remaining := RetryWindow - e.o.Clock.Now().Sub(started)
			if remaining <= 0 {
				e.o.Diag.Debug("dropped %d records after retrying for 5 minutes: %s", len(batch), out.reason)
				return
			}
			wait := backoff(attempt, e.o.Rand())
			if out.hasAfter {
				wait = max(out.retryAfter, retryAfterFloor)
			}
			wait = min(wait, remaining)
			e.o.Diag.Debug("export failed (%s); retrying in %v", out.reason, wait.Round(time.Millisecond))
			if !e.o.Clock.Sleep(e.ctx, wait) {
				return
			}
		}
	}
}

// Full jitter: a random share of 1 s, 2 s, 4 s … capped at 30 s.
func backoff(attempt int, r float64) time.Duration {
	d := backoffCap
	if attempt < 5 {
		d = min(backoffCap, backoffBase<<attempt)
	}
	return time.Duration(r * float64(d))
}

func (e *Exporter) post(batch []encode.Encoded) outcome {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(encode.Request(e.o.Resource, batch))
	if err := zw.Close(); err != nil {
		return outcome{kind: drop, reason: "compression failed: " + err.Error()}
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.o.URL, bytes.NewReader(gz.Bytes()))
	if err != nil {
		return outcome{kind: drop, reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer "+e.o.APIKey)
	req.Header.Set("User-Agent", e.o.UserAgent)
	resp, err := e.o.HTTPClient.Do(req)
	if err != nil {
		if e.ctx.Err() != nil {
			return outcome{kind: drop, reason: "closed"}
		}
		return outcome{kind: retry, reason: err.Error()}
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		if location == "" {
			location = "an unknown location"
		}
		e.o.Diag.WarnOnce("redirect", "the endpoint %s redirected to %s; set LOGIFY_ENDPOINT to the final URL. Logs are dropped until then", e.o.URL, location)
		return outcome{kind: drop, reason: "redirect to " + location}
	}
	return classify(resp.StatusCode, resp.Header.Get("Retry-After"), string(text), e.o.Clock.Now())
}

func (e *Exporter) reportPartial(body string) {
	if !e.o.Diag.DebugEnabled() || body == "" {
		return
	}
	var parsed map[string]map[string]any
	if json.Unmarshal([]byte(body), &parsed) != nil {
		return
	}
	partial := parsed["partialSuccess"]
	if partial == nil {
		partial = parsed["partial_success"]
	}
	if partial == nil {
		return
	}
	rejected := partial["rejectedLogRecords"]
	if rejected == nil {
		rejected = partial["rejected_log_records"]
	}
	message := partial["errorMessage"]
	if message == nil {
		message = partial["error_message"]
	}
	e.o.Diag.Debug("partial success: %v records rejected: %v", rejected, message)
}

// 401/403: the key is invalid, revoked or lacks the scope.
func (e *Exporter) stop(status int) {
	e.mu.Lock()
	e.stopped = true
	e.clearLocked()
	e.mu.Unlock()
	e.o.Diag.WarnOnce("auth", "the API key was rejected (HTTP %d); Logify stops sending logs for this process", status)
}
