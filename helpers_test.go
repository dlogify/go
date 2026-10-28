package logify_test

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

type sentRecord struct {
	Scope    string
	Severity int
	Text     string
	Body     string
	Time     string
	Attrs    map[string]any // string, int64, float64 or bool
}

type ingest struct {
	*httptest.Server
	mu       sync.Mutex
	records  []sentRecord
	resource map[string]any
}

type otlpAttr struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func decodeAttrs(list []otlpAttr) map[string]any {
	out := map[string]any{}
	for _, a := range list {
		switch {
		case a.Value["stringValue"] != nil:
			out[a.Key] = a.Value["stringValue"]
		case a.Value["intValue"] != nil:
			n, _ := strconv.ParseInt(a.Value["intValue"].(string), 10, 64)
			out[a.Key] = n
		case a.Value["doubleValue"] != nil:
			out[a.Key] = a.Value["doubleValue"]
		case a.Value["boolValue"] != nil:
			out[a.Key] = a.Value["boolValue"]
		}
	}
	return out
}

func newIngest(t *testing.T) *ingest {
	t.Helper()
	s := &ingest{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip: %v", err)
			return
		}
		raw, _ := io.ReadAll(zr)
		var body struct {
			ResourceLogs []struct {
				Resource  struct{ Attributes []otlpAttr }
				ScopeLogs []struct {
					Scope      struct{ Name string }
					LogRecords []struct {
						TimeUnixNano   string
						SeverityNumber int
						SeverityText   string
						Body           struct{ StringValue string }
						Attributes     []otlpAttr
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
			s.resource = decodeAttrs(rl.Resource.Attributes)
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					s.records = append(s.records, sentRecord{
						Scope: sl.Scope.Name, Severity: lr.SeverityNumber, Text: lr.SeverityText,
						Body: lr.Body.StringValue, Time: lr.TimeUnixNano, Attrs: decodeAttrs(lr.Attributes),
					})
				}
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *ingest) all() []sentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentRecord(nil), s.records...)
}

func (s *ingest) bodies() []string {
	var out []string
	for _, r := range s.all() {
		out = append(out, r.Body)
	}
	return out
}

func newClient(t *testing.T, s *ingest, opts logify.Options) *logify.Client {
	t.Helper()
	opts.APIKey = "lgf_test"
	opts.Endpoint = s.URL
	c := logify.New(opts)
	t.Cleanup(func() { c.Shutdown(time.Second) })
	return c
}

func flushed(t *testing.T, c *logify.Client, s *ingest) []sentRecord {
	t.Helper()
	if !c.Flush(2 * time.Second) {
		t.Fatal("flush timed out")
	}
	return s.all()
}
