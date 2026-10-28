package parse

import (
	"strings"
	"testing"
	"time"
)

func TestDetectLevel(t *testing.T) {
	cases := map[string]int{
		"ERROR:root:boom":                      17,
		"[warn] disk 91%":                      13,
		"2026-09-30 12:00:01,123 INFO started": 9,
		"app-1  | ERROR failed to connect":     17,
		"(debug) x":                            5,
		"warning: deprecated":                  13,
	}
	for line, want := range cases {
		if got, ok := DetectLevel(line); !ok || got != want {
			t.Fatalf("%q: %d %v", line, got, ok)
		}
	}
	for _, line := range []string{"no level here", "information", "xERROR y", strings.Repeat("a", 70) + " ERROR"} {
		if got, ok := DetectLevel(line); ok {
			t.Fatalf("%q: unexpected %d", line, got)
		}
	}
}

func TestEventLimitsMarkTruncation(t *testing.T) {
	var out []Parsed
	now := time.Unix(0, 0)
	p := NewStreamParser("stderr", func() time.Time { return now }, func(r Parsed) { out = append(out, r) })
	var b strings.Builder
	b.WriteString("Error: boom\n")
	for i := 0; i < MaxEventLines+5; i++ {
		b.WriteString("    at f (/a.js:1:1)\n")
	}
	p.Push([]byte(b.String()))
	p.End()
	if len(out) < 2 {
		t.Fatalf("got %d records", len(out))
	}
	found := false
	for _, a := range out[0].Attrs {
		if a.Key == "logify.truncated" && a.Value.Bool {
			found = true
		}
	}
	if !found {
		t.Fatal("first event not marked truncated")
	}
}

func FuzzStreamParser(f *testing.F) {
	f.Add([]byte("Error: boom\n    at f (/a.js:1:1)\n{\"msg\":\"x\",\"level\":30}\npanic: x\n\ngoroutine 1 [running]:\n"))
	f.Add([]byte("Traceback (most recent call last):\n  File \"a.py\", line 1\nValueError: x\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		now := time.Unix(1, 0)
		p := NewStreamParser("stdout", func() time.Time { return now }, func(r Parsed) {
			if r.Time == 0 || len(r.Attrs) == 0 || r.Attrs[0].Key != "log.iostream" {
				t.Fatalf("bad record %+v", r)
			}
		})
		p.Push(data)
		now = now.Add(time.Second)
		p.Tick()
		p.End()
	})
}
