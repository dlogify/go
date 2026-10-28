package export

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dlogify.com/sdk/go/internal/diag"
	"dlogify.com/sdk/go/internal/record"
)

type request struct {
	header http.Header
	path   string
	bodies []string // record bodies, in order
}

type server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []request
	reply    func(n int, bodies []string, w http.ResponseWriter) // n: 0-based request number
	got      chan struct{}
}

func newServer(t *testing.T, reply func(n int, bodies []string, w http.ResponseWriter)) *server {
	s := &server{reply: reply, got: make(chan struct{}, 1000)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip: %v", err)
			return
		}
		raw, _ := io.ReadAll(zr)
		var body struct {
			ResourceLogs []struct {
				ScopeLogs []struct {
					LogRecords []struct {
						Body struct{ StringValue string }
					}
				}
			}
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("json: %v: %s", err, raw)
		}
		var bodies []string
		for _, rl := range body.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					bodies = append(bodies, lr.Body.StringValue)
				}
			}
		}
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, request{header: r.Header.Clone(), path: r.URL.Path, bodies: bodies})
		s.mu.Unlock()
		if s.reply != nil {
			s.reply(n, bodies, w)
		}
		s.got <- struct{}{}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) all() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]request(nil), s.requests...)
}

// fakeClock: Sleep advances Now and records the duration.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) bool {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.sleeps = append(c.sleeps, d)
	c.mu.Unlock()
	return ctx.Err() == nil
}

func newExporter(t *testing.T, s *server, mod func(*Options)) *Exporter {
	o := Options{URL: s.URL + "/v1/logs", APIKey: "lgf_test", UserAgent: "logify-go/test", MaxQueueSize: 2000, Diag: diag.New(false)}
	if mod != nil {
		mod(&o)
	}
	e := New(o)
	t.Cleanup(e.Close)
	return e
}

func rec(body string) *record.Record {
	return &record.Record{Scope: "logify", Severity: record.SeverityError, Body: body, Time: 1, Observed: 2}
}

func TestSendsGzipJSONWithHeaders(t *testing.T) {
	s := newServer(t, nil)
	e := newExporter(t, s, nil)
	e.Enqueue(rec("hello"))
	if !e.Flush(2 * time.Second) {
		t.Fatal("flush timed out")
	}
	reqs := s.all()
	if len(reqs) != 1 || reqs[0].path != "/v1/logs" || reqs[0].bodies[0] != "hello" {
		t.Fatalf("got %+v", reqs)
	}
	h := reqs[0].header
	if h.Get("Authorization") != "Bearer lgf_test" || h.Get("Content-Type") != "application/json" ||
		h.Get("Content-Encoding") != "gzip" || h.Get("User-Agent") != "logify-go/test" {
		t.Fatalf("headers %v", h)
	}
}

func TestFlushWithNothingQueuedReturnsAtOnce(t *testing.T) {
	s := newServer(t, nil)
	e := newExporter(t, s, nil)
	if !e.Flush(time.Millisecond) {
		t.Fatal("empty flush must succeed")
	}
}

func TestSizeTriggerSendsWithoutFlush(t *testing.T) {
	s := newServer(t, nil)
	e := newExporter(t, s, func(o *Options) { o.Interval = time.Hour })
	for i := 0; i < TriggerRecords; i++ {
		e.Enqueue(rec("x"))
	}
	select {
	case <-s.got:
	case <-time.After(2 * time.Second):
		t.Fatal("no request after 500 records")
	}
}

func TestIntervalSends(t *testing.T) {
	s := newServer(t, nil)
	e := newExporter(t, s, func(o *Options) { o.Interval = 20 * time.Millisecond })
	e.Enqueue(rec("x"))
	select {
	case <-s.got:
	case <-time.After(2 * time.Second):
		t.Fatal("no periodic export")
	}
}

func TestBatchLimits(t *testing.T) {
	s := newServer(t, nil)
	e := newExporter(t, s, func(o *Options) { o.Interval = time.Hour; o.MaxQueueSize = 3000 })
	e.mu.Lock()
	e.started = true // keep the loop from sending while we queue
	e.mu.Unlock()
	for i := 0; i < 2500; i++ {
		e.Enqueue(rec("x"))
	}
	e.mu.Lock()
	e.started = false
	e.mu.Unlock()
	e.ensureLoop()
	if !e.Flush(5 * time.Second) {
		t.Fatal("flush timed out")
	}
	sizes := []int{}
	for _, r := range s.all() {
		sizes = append(sizes, len(r.bodies))
	}
	if len(sizes) != 3 || sizes[0] != BatchMaxRecords || sizes[1] != BatchMaxRecords || sizes[2] != 500 {
		t.Fatalf("batch sizes %v", sizes)
	}
}

func TestQueueOverflowDropsOldest(t *testing.T) {
	s := newServer(t, nil)
	e := newExporter(t, s, func(o *Options) { o.MaxQueueSize = 3; o.Interval = time.Hour })
	e.mu.Lock()
	e.started = true
	e.mu.Unlock()
	for _, b := range []string{"1", "2", "3", "4", "5"} {
		e.Enqueue(rec(b))
	}
	e.mu.Lock()
	e.started = false
	dropped := e.dropped
	e.mu.Unlock()
	e.ensureLoop()
	e.Flush(2 * time.Second)
	if got := strings.Join(s.all()[0].bodies, ","); got != "3,4,5" || dropped != 2 {
		t.Fatalf("sent %s, dropped %d", got, dropped)
	}
}

func TestRetryAfterThenSuccess(t *testing.T) {
	s := newServer(t, func(n int, _ []string, w http.ResponseWriter) {
		if n == 0 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})
	clock := &fakeClock{now: time.Unix(1_790_000_000, 0)}
	e := newExporter(t, s, func(o *Options) { o.Clock = clock })
	e.Enqueue(rec("x"))
	if !e.Flush(2 * time.Second) {
		t.Fatal("flush timed out")
	}
	if len(s.all()) != 2 || len(clock.sleeps) != 1 || clock.sleeps[0] != 3*time.Second {
		t.Fatalf("requests %d, sleeps %v", len(s.all()), clock.sleeps)
	}
}

func TestBackoffGivesUpAfterFiveMinutes(t *testing.T) {
	s := newServer(t, func(_ int, _ []string, w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) })
	clock := &fakeClock{now: time.Unix(1_790_000_000, 0)}
	e := newExporter(t, s, func(o *Options) { o.Clock = clock; o.Rand = func() float64 { return 1 } })
	e.Enqueue(rec("x"))
	if !e.Flush(5 * time.Second) {
		t.Fatal("flush timed out")
	}
	var total time.Duration
	for _, d := range clock.sleeps {
		total += d
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}
	for i, d := range want {
		if clock.sleeps[i] != d {
			t.Fatalf("sleeps %v", clock.sleeps)
		}
	}
	if total != RetryWindow {
		t.Fatalf("slept %v in total", total)
	}
}

func Test413SplitsTheBatch(t *testing.T) {
	var mu sync.Mutex
	accepted := []string{}
	s := newServer(t, func(_ int, bodies []string, w http.ResponseWriter) {
		if len(bodies) > 1 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		mu.Lock()
		accepted = append(accepted, bodies...)
		mu.Unlock()
	})
	e := newExporter(t, s, nil)
	e.mu.Lock()
	e.started = true
	e.mu.Unlock()
	for _, b := range []string{"a", "b", "c"} {
		e.Enqueue(rec(b))
	}
	e.mu.Lock()
	e.started = false
	e.mu.Unlock()
	e.ensureLoop()
	e.Flush(2 * time.Second)
	if strings.Join(accepted, "") != "abc" {
		t.Fatalf("accepted %v", accepted)
	}
}

func Test401StopsForGood(t *testing.T) {
	var out bytes.Buffer
	defer diag.SetOutput(&out)()
	s := newServer(t, func(_ int, _ []string, w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) })
	e := newExporter(t, s, nil)
	e.Enqueue(rec("x"))
	e.Flush(2 * time.Second)
	e.Enqueue(rec("y"))
	e.Flush(200 * time.Millisecond)
	if len(s.all()) != 1 {
		t.Fatalf("requests after 401: %d", len(s.all()))
	}
	if strings.Count(out.String(), "rejected") != 1 {
		t.Fatalf("warning: %q", out.String())
	}
}

func TestBadRequestDropsTheBatch(t *testing.T) {
	s := newServer(t, func(n int, _ []string, w http.ResponseWriter) {
		if n == 0 {
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	e := newExporter(t, s, nil)
	e.Enqueue(rec("bad"))
	e.Flush(2 * time.Second)
	e.Enqueue(rec("good"))
	e.Flush(2 * time.Second)
	reqs := s.all()
	if len(reqs) != 2 || reqs[1].bodies[0] != "good" {
		t.Fatalf("got %+v", reqs)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var out bytes.Buffer
	defer diag.SetOutput(&out)()
	s := newServer(t, func(_ int, _ []string, w http.ResponseWriter) {
		w.Header().Set("Location", "https://elsewhere.example/v1/logs")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	e := newExporter(t, s, nil)
	e.Enqueue(rec("x"))
	e.Flush(2 * time.Second)
	if len(s.all()) != 1 || !strings.Contains(out.String(), "redirected") {
		t.Fatalf("requests %d, output %q", len(s.all()), out.String())
	}
}

func TestFlushTimesOutOnHangingServer(t *testing.T) {
	release := make(chan struct{})
	s := newServer(t, func(int, []string, http.ResponseWriter) { <-release })
	defer close(release)
	e := newExporter(t, s, nil)
	e.Enqueue(rec("x"))
	start := time.Now()
	if e.Flush(100 * time.Millisecond) {
		t.Fatal("flush must time out")
	}
	if time.Since(start) > time.Second {
		t.Fatal("flush took too long")
	}
	start = time.Now()
	e.Close()
	if time.Since(start) > time.Second {
		t.Fatal("close waited for the hanging request")
	}
}

func TestCloseStopsRetrySleep(t *testing.T) {
	s := newServer(t, func(_ int, _ []string, w http.ResponseWriter) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	e := newExporter(t, s, nil)
	e.Enqueue(rec("x"))
	<-s.got
	start := time.Now()
	e.Close()
	if time.Since(start) > time.Second {
		t.Fatal("close waited for the retry sleep")
	}
	e.Enqueue(rec("after close")) // ignored, no panic
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if d, ok := parseRetryAfter("7", now); !ok || d != 7*time.Second {
		t.Fatal("seconds")
	}
	if d, ok := parseRetryAfter("Wed, 28 Oct 2026 12:00:10 GMT", now); !ok || d != 10*time.Second {
		t.Fatalf("date: %v", d)
	}
	if _, ok := parseRetryAfter("soon", now); ok {
		t.Fatal("garbage")
	}
}
