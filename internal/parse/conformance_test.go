package parse_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"dlogify.com/sdk/go/internal/conformance"
	"dlogify.com/sdk/go/internal/parse"
	"dlogify.com/sdk/go/internal/record"
)

type step struct {
	Stream  string   `json:"stream"`
	Lines   []string `json:"lines"`
	PauseMs int      `json:"pause_ms"`
}

type expected struct {
	Stream         string         `json:"stream"`
	SeverityNumber int            `json:"severity_number"`
	SeverityText   string         `json:"severity_text"`
	Body           string         `json:"body"`
	TimeUnixNano   string         `json:"time_unix_nano"`
	Attributes     map[string]any `json:"attributes"`
}

func TestParsingConformance(t *testing.T) {
	dir := conformance.Dir(t, "parsing")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Input    []step     `json:"input"`
				Expected []expected `json:"expected"`
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			now := time.Unix(1_790_000_000, 0)
			clock := func() time.Time { return now }
			got := map[string][]parse.Parsed{}
			parsers := map[string]*parse.StreamParser{}
			for _, name := range []string{"stdout", "stderr"} {
				name := name
				parsers[name] = parse.NewStreamParser(name, clock, func(r parse.Parsed) { got[name] = append(got[name], r) })
			}
			for _, s := range fixture.Input {
				if s.PauseMs > 0 {
					now = now.Add(time.Duration(s.PauseMs) * time.Millisecond)
					parsers["stdout"].Tick()
					parsers["stderr"].Tick()
					continue
				}
				for _, line := range s.Lines {
					parsers[s.Stream].Push([]byte(line + "\n"))
				}
			}
			parsers["stdout"].End()
			parsers["stderr"].End()

			want := map[string][]expected{}
			for _, e := range fixture.Expected {
				want[e.Stream] = append(want[e.Stream], e)
			}
			for _, stream := range []string{"stdout", "stderr"} {
				if len(got[stream]) != len(want[stream]) {
					t.Fatalf("%s: got %d records, want %d: %+v", stream, len(got[stream]), len(want[stream]), got[stream])
				}
				for i, w := range want[stream] {
					compare(t, stream, i, got[stream][i], w)
				}
			}
		})
	}
}

func compare(t *testing.T, stream string, i int, got parse.Parsed, want expected) {
	t.Helper()
	if got.Severity != want.SeverityNumber || record.SeverityText(got.Severity) != want.SeverityText {
		t.Fatalf("%s[%d] severity: got %d %q, want %d %q", stream, i, got.Severity, record.SeverityText(got.Severity), want.SeverityNumber, want.SeverityText)
	}
	if got.Body != want.Body {
		t.Fatalf("%s[%d] body: got %q, want %q", stream, i, got.Body, want.Body)
	}
	if want.TimeUnixNano != "" && strconv.FormatInt(got.Time, 10) != want.TimeUnixNano {
		t.Fatalf("%s[%d] time: got %d, want %s", stream, i, got.Time, want.TimeUnixNano)
	}
	if len(got.Attrs) != len(want.Attributes) {
		t.Fatalf("%s[%d] attributes: got %+v, want %+v", stream, i, got.Attrs, want.Attributes)
	}
	for _, a := range got.Attrs {
		w, ok := want.Attributes[a.Key]
		if !ok || !sameValue(a.Value, w) {
			t.Fatalf("%s[%d] attribute %q: got %+v, want %#v", stream, i, a.Key, a.Value, w)
		}
	}
}

// Numbers compare by value; an integer must be an int, a fraction a double.
func sameValue(v record.Value, w any) bool {
	switch x := w.(type) {
	case string:
		return v.Kind == record.KindString && v.Str == x
	case bool:
		return v.Kind == record.KindBool && v.Bool == x
	case float64:
		if x == math.Trunc(x) {
			return v.Kind == record.KindInt && float64(v.Int) == x
		}
		return v.Kind == record.KindDouble && v.Float == x
	}
	return false
}
