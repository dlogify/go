package logify_test

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	logify "dlogify.com/sdk/go"
)

type stringer struct{}

func (stringer) String() string { return "custom value" }

func TestRecoverRepanicsSameValue(t *testing.T) {
	errValue := errors.New("boom")
	var nilErr *panickyErrorPtr
	cases := []struct {
		value   any
		typ     string
		message string
	}{
		{errValue, "*errors.errorString", "boom"},
		{"text", "panic", "text"},
		{42, "panic", "42"},
		{stringer{}, "panic", "custom value"},
		{nilErr, "*logify_test.panickyErrorPtr", "*logify_test.panickyErrorPtr"},
	}
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	for _, tc := range cases {
		var got any
		func() {
			defer func() { got = recover() }()
			func() {
				defer c.Recover()
				panic(tc.value)
			}()
		}()
		if got != tc.value {
			t.Fatalf("re-panicked with %#v, want %#v", got, tc.value)
		}
	}
	records := flushed(t, c, s)
	if len(records) != len(cases) {
		t.Fatalf("got %d records", len(records))
	}
	for i, tc := range cases {
		r := records[i]
		trace, _ := r.Attrs["exception.stacktrace"].(string)
		if r.Scope != "uncaught" || r.Severity != 21 || r.Body != "panic: "+tc.message ||
			r.Attrs["exception.type"] != tc.typ || r.Attrs["exception.message"] != tc.message || r.Attrs["exception.origin"] != "panic" ||
			!strings.HasPrefix(trace, "panic: "+tc.message+"\n\ngoroutine ") ||
			!strings.Contains(trace, "\ndlogify.com/sdk/go_test.TestRecoverRepanicsSameValue.func") ||
			strings.Contains(trace, "runtime.gopanic") || strings.Contains(trace, "dlogify.com/sdk/go.(*Client)") {
			t.Fatalf("record %d: %+v\n%s", i, r, trace)
		}
	}
}

func TestRecoverWithoutPanicDoesNothing(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	func() { defer c.Recover() }()
	if len(flushed(t, c, s)) != 0 {
		t.Fatal("record without a panic")
	}
}

func TestPackageRecoverRepanics(t *testing.T) {
	var got any
	func() {
		defer func() { got = recover() }()
		func() {
			defer logify.Recover()
			panic("x")
		}()
	}()
	if got != "x" {
		t.Fatalf("got %#v", got)
	}
}

// The child process (same test binary) panics inside c.Go; the goroutine's
// re-panic ends it, as it would without dlogify.
func TestGoRecoversInGoroutine(t *testing.T) {
	if endpoint := os.Getenv("LOGIFY_TEST_GO_PANIC"); endpoint != "" {
		c := logify.New(logify.Options{APIKey: "lgf_test", Endpoint: endpoint})
		c.Go(func() { panic("in goroutine") })
		time.Sleep(10 * time.Second)
		return
	}
	s := newIngest(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoRecoversInGoroutine$")
	cmd.Env = append(os.Environ(), "LOGIFY_TEST_GO_PANIC="+s.URL)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "panic: in goroutine") {
		t.Fatalf("child: %v\n%s", err, out)
	}
	got := s.all()
	if len(got) != 1 || got[0].Body != "panic: in goroutine" || got[0].Severity != 21 {
		t.Fatalf("got %+v", got)
	}
}

func TestMiddleware(t *testing.T) {
	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic(errors.New("handler boom")) })
	mux.HandleFunc("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	srv := httptest.NewUnstartedServer(c.Middleware(mux))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Start()
	defer srv.Close()
	for _, path := range []string{"/boom", "/abort", "/ok"} {
		resp, err := http.Get(srv.URL + path)
		if err == nil {
			resp.Body.Close()
		}
	}
	got := flushed(t, c, s)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	r := got[0]
	if r.Severity != 17 || r.Body != "panic: handler boom" || r.Attrs["http.request.method"] != "GET" ||
		r.Attrs["url.path"] != "/boom" || r.Attrs["exception.type"] != "*errors.errorString" || r.Attrs["exception.origin"] != "http" {
		t.Fatalf("got %+v", r)
	}
}
