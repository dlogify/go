// Package ingesttest is a fake OTLP/HTTP ingest for the adapters' tests.
package ingesttest

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	logify "dlogify.com/sdk/go"
)

type Record struct {
	Scope    string
	Severity int
	Body     string
	Attrs    map[string]any // string, int64, float64 or bool
}

type Server struct {
	*httptest.Server
	mu      sync.Mutex
	records []Record
}

type attr struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{}
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
					Scope      struct{ Name string }
					LogRecords []struct {
						SeverityNumber int
						Body           struct{ StringValue string }
						Attributes     []attr
					}
				}
			}
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("json: %v", err)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, rl := range body.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					attrs := map[string]any{}
					for _, a := range lr.Attributes {
						switch {
						case a.Value["intValue"] != nil:
							n, _ := strconv.ParseInt(a.Value["intValue"].(string), 10, 64)
							attrs[a.Key] = n
						case a.Value["stringValue"] != nil:
							attrs[a.Key] = a.Value["stringValue"]
						case a.Value["doubleValue"] != nil:
							attrs[a.Key] = a.Value["doubleValue"]
						case a.Value["boolValue"] != nil:
							attrs[a.Key] = a.Value["boolValue"]
						}
					}
					s.records = append(s.records, Record{Scope: sl.Scope.Name, Severity: lr.SeverityNumber, Body: lr.Body.StringValue, Attrs: attrs})
				}
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

func (s *Server) Client(t testing.TB) *logify.Client {
	c := logify.New(logify.Options{APIKey: "lgf_test", Endpoint: s.URL})
	t.Cleanup(func() { c.Shutdown(time.Second) })
	return c
}
