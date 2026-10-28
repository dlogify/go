package logify_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	logify "dlogify.com/sdk/go"
	"dlogify.com/sdk/go/internal/conformance"
)

type redactionFixture struct {
	Options struct {
		RedactDefaults bool     `json:"redact_defaults"`
		RedactKeys     []string `json:"redact_keys"`
		RedactPatterns []struct {
			Pattern     string `json:"pattern"`
			Flags       string `json:"flags"`
			Replacement string `json:"replacement"`
		} `json:"redact_patterns"`
	} `json:"options"`
	Cases []struct {
		Input struct {
			Body       string         `json:"body"`
			Attributes map[string]any `json:"attributes"`
		} `json:"input"`
		Expected struct {
			Body       string         `json:"body"`
			Attributes map[string]any `json:"attributes"`
		} `json:"expected"`
	} `json:"cases"`
}

func slogAttrs(m map[string]any) []slog.Attr {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []slog.Attr
	for _, k := range keys {
		out = append(out, slog.Any(k, m[k]))
	}
	return out
}

func expectedValue(v any) any {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
		f, _ := n.Float64()
		return f
	}
	return v
}

func TestRedactionConformance(t *testing.T) {
	dir := conformance.Dir(t, "redaction")
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
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			var f redactionFixture
			if err := dec.Decode(&f); err != nil {
				t.Fatal(err)
			}
			opts := logify.Options{RedactDefaults: f.Options.RedactDefaults, RedactKeys: f.Options.RedactKeys}
			for _, p := range f.Options.RedactPatterns {
				pattern := p.Pattern
				if p.Flags != "" {
					pattern = "(?" + p.Flags + ")" + pattern
				}
				opts.RedactPatterns = append(opts.RedactPatterns, logify.RedactPattern{Pattern: regexp.MustCompile(pattern), Replacement: p.Replacement})
			}
			s := newIngest(t)
			c := newClient(t, s, opts)
			for _, tc := range f.Cases {
				c.Info(tc.Input.Body, slogAttrs(tc.Input.Attributes)...)
			}
			got := flushed(t, c, s)
			if len(got) != len(f.Cases) {
				t.Fatalf("got %d records, want %d", len(got), len(f.Cases))
			}
			for i, tc := range f.Cases {
				if got[i].Body != tc.Expected.Body {
					t.Errorf("case %d body:\n got %q\nwant %q", i, got[i].Body, tc.Expected.Body)
				}
				if len(got[i].Attrs) != len(tc.Expected.Attributes) {
					t.Errorf("case %d attributes: got %v, want %v", i, got[i].Attrs, tc.Expected.Attributes)
				}
				for k, v := range tc.Expected.Attributes {
					if got[i].Attrs[k] != expectedValue(v) {
						t.Errorf("case %d attribute %s: got %#v, want %#v", i, k, got[i].Attrs[k], expectedValue(v))
					}
				}
			}
		})
	}
}
