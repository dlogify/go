package record

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNormalize(t *testing.T) {
	type custom struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	cases := []struct {
		name string
		in   any
		want Value
	}{
		{"string", "x", String("x")},
		{"bool", true, Bool(true)},
		{"int", 3, Int(3)},
		{"int8", int8(-3), Int(-3)},
		{"uint64 small", uint64(7), Int(7)},
		{"uint64 huge", uint64(math.MaxUint64), Double(float64(math.MaxUint64))},
		{"float", 0.5, Double(0.5)},
		{"float32", float32(0.25), Double(0.25)},
		{"nan", math.NaN(), String("NaN")},
		{"inf", math.Inf(1), String("+Inf")},
		{"duration", 1500 * time.Millisecond, Int(1_500_000_000)},
		{"time", time.Date(2026, 10, 28, 12, 0, 0, 5, time.UTC), String("2026-10-28T12:00:00.000000005Z")},
		{"error", errors.New("boom"), String("boom")},
		{"json.Number int", json.Number("12"), Int(12)},
		{"json.Number float", json.Number("1.5"), Double(1.5)},
		{"struct", custom{A: 1, B: "x"}, String(`{"a":1,"b":"x"}`)},
		{"map", map[string]int{"b": 2, "a": 1}, String(`{"a":1,"b":2}`)},
		{"slice", []int{1, 2}, String(`[1,2]`)},
		{"channel", make(chan int), String("")}, // replaced below: fmt.Sprint of a channel is an address
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Normalize(c.in)
			if !ok {
				t.Fatal("not ok")
			}
			if c.name == "channel" {
				if got.Kind != KindString || !strings.HasPrefix(got.Str, "0x") {
					t.Fatalf("got %#v", got)
				}
				return
			}
			if got != c.want {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
	if _, ok := Normalize(nil); ok {
		t.Fatal("nil must be skipped")
	}
	var nilErr *testError
	if v, ok := Normalize(nilErr); !ok || v.Kind != KindString {
		t.Fatalf("typed nil error: %#v %v", v, ok)
	}
}

type testError struct{}

func (*testError) Error() string { panic("nil receiver") }

type panicky struct{}

func (panicky) MarshalJSON() ([]byte, error) { panic("boom") }
func (panicky) String() string               { panic("boom") }

func TestNormalizeSurvivesPanickingMethods(t *testing.T) {
	v, ok := Normalize(panicky{})
	if !ok || v.Kind != KindString {
		t.Fatalf("got %#v", v)
	}
}

func TestSetKeepsFirstPositionAndLastValue(t *testing.T) {
	attrs := Set(nil, "a", Int(1))
	attrs = Set(attrs, "b", Int(2))
	attrs = Set(attrs, "a", Int(3))
	if len(attrs) != 2 || attrs[0].Key != "a" || attrs[0].Value != Int(3) || attrs[1].Key != "b" {
		t.Fatalf("got %#v", attrs)
	}
}

func TestCutUTF8(t *testing.T) {
	if got := CutUTF8("héllo", 2); got != "h" {
		t.Fatalf("got %q", got)
	}
	if got := CutUTF8("héllo", 3); got != "hé" {
		t.Fatalf("got %q", got)
	}
	if got := CutUTF8("abc", 10); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestFinalizeCutsAndMarks(t *testing.T) {
	r := &Record{
		Body: strings.Repeat("a", BodyMaxBytes+10),
		Attrs: []Attr{
			{"exception.stacktrace", String(strings.Repeat("s", StacktraceMaxBytes+1))},
			{"other", String(strings.Repeat("o", AttributeMaxBytes+1))},
			{"n", Int(1)},
		},
	}
	Finalize(r)
	if len(r.Body) != BodyMaxBytes {
		t.Fatalf("body %d", len(r.Body))
	}
	if len(r.Attrs[0].Value.Str) != StacktraceMaxBytes || len(r.Attrs[1].Value.Str) != AttributeMaxBytes {
		t.Fatal("attributes not cut")
	}
	if v, ok := Get(r.Attrs, "logify.truncated"); !ok || v != Bool(true) {
		t.Fatal("logify.truncated missing")
	}
}

func TestFinalizeLeavesSmallRecordsAlone(t *testing.T) {
	r := &Record{Body: "ok", Attrs: []Attr{{"a", String("b")}}}
	Finalize(r)
	if _, ok := Get(r.Attrs, "logify.truncated"); ok {
		t.Fatal("unexpected logify.truncated")
	}
}

func TestFinalizeCutsInvalidUTF8(t *testing.T) {
	body := strings.Repeat("é", BodyMaxBytes) + "\xff\xfe"
	r := &Record{Body: body}
	Finalize(r)
	if len(r.Body) > BodyMaxBytes || !utf8.ValidString(r.Body) {
		t.Fatalf("body %d bytes, valid %v", len(r.Body), utf8.ValidString(r.Body))
	}
}

func TestSeverityText(t *testing.T) {
	for n, want := range map[int]string{1: "TRACE", 5: "DEBUG", 9: "INFO", 13: "WARN", 17: "ERROR", 21: "FATAL", 0: ""} {
		if got := SeverityText(n); got != want {
			t.Fatalf("%d: %q", n, got)
		}
	}
}

func TestExceptionTitle(t *testing.T) {
	if ExceptionTitle("T", "m") != "T: m" || ExceptionTitle("T", "") != "T" {
		t.Fatal("title")
	}
}
