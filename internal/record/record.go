// Package record holds the client's log record and the value rules of
// docs/12 §2–3: typed attribute values, normalization of arbitrary Go values,
// and the size cuts applied before a record is queued.
package record

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"
)

type Kind uint8

const (
	KindString Kind = iota
	KindInt
	KindDouble
	KindBool
)

// Value is an OTLP AnyValue restricted to what clients send (docs/12 §2).
type Value struct {
	Kind  Kind
	Str   string
	Int   int64
	Float float64
	Bool  bool
}

func String(s string) Value  { return Value{Kind: KindString, Str: s} }
func Int(i int64) Value      { return Value{Kind: KindInt, Int: i} }
func Double(f float64) Value { return Value{Kind: KindDouble, Float: f} }
func Bool(b bool) Value      { return Value{Kind: KindBool, Bool: b} }

// Native returns the value as string, int64, float64 or bool.
func (v Value) Native() any {
	switch v.Kind {
	case KindInt:
		return v.Int
	case KindDouble:
		return v.Float
	case KindBool:
		return v.Bool
	default:
		return v.Str
	}
}

type Attr struct {
	Key   string
	Value Value
}

// Set replaces the value of an existing key in place, else appends: a key
// keeps its first position and its last value.
func Set(attrs []Attr, key string, v Value) []Attr {
	for i := range attrs {
		if attrs[i].Key == key {
			attrs[i].Value = v
			return attrs
		}
	}
	return append(attrs, Attr{Key: key, Value: v})
}

func Get(attrs []Attr, key string) (Value, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return Value{}, false
}

// Severity numbers (docs/12 §3); 0 is an unknown level (only logify run).
const (
	SeverityUnknown = 0
	SeverityTrace   = 1
	SeverityDebug   = 5
	SeverityInfo    = 9
	SeverityWarn    = 13
	SeverityError   = 17
	SeverityFatal   = 21
)

func SeverityText(n int) string {
	switch n {
	case SeverityTrace:
		return "TRACE"
	case SeverityDebug:
		return "DEBUG"
	case SeverityInfo:
		return "INFO"
	case SeverityWarn:
		return "WARN"
	case SeverityError:
		return "ERROR"
	case SeverityFatal:
		return "FATAL"
	}
	return ""
}

// Record is one log record; times are Unix nanoseconds.
type Record struct {
	Scope    string
	Time     int64
	Observed int64
	Severity int
	Body     string
	Attrs    []Attr
}

func ExceptionTitle(typ, msg string) string {
	if msg == "" {
		return typ
	}
	return typ + ": " + msg
}

// Normalize maps a Go value to an attribute value: strings, booleans,
// integers and finite floats keep their type, durations become nanoseconds,
// times RFC 3339 strings, errors their text, anything else its JSON (or
// fmt.Sprint when it has none). nil is skipped.
func Normalize(v any) (out Value, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = String(fmt.Sprintf("%T", v)), true
		}
	}()
	switch x := v.(type) {
	case nil:
		return Value{}, false
	case string:
		return String(x), true
	case bool:
		return Bool(x), true
	case int:
		return Int(int64(x)), true
	case int8:
		return Int(int64(x)), true
	case int16:
		return Int(int64(x)), true
	case int32:
		return Int(int64(x)), true
	case int64:
		return Int(x), true
	case uint:
		return fromUint(uint64(x)), true
	case uint8:
		return Int(int64(x)), true
	case uint16:
		return Int(int64(x)), true
	case uint32:
		return Int(int64(x)), true
	case uint64:
		return fromUint(x), true
	case uintptr:
		return fromUint(uint64(x)), true
	case float32:
		return fromFloat(float64(x)), true
	case float64:
		return fromFloat(x), true
	case time.Duration:
		return Int(int64(x)), true
	case time.Time:
		return String(x.Format(time.RFC3339Nano)), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return Int(i), true
		}
		if f, err := x.Float64(); err == nil {
			return fromFloat(f), true
		}
		return String(x.String()), true
	case error:
		return String(x.Error()), true
	}
	if b, err := json.Marshal(v); err == nil {
		return String(string(b)), true
	}
	return String(fmt.Sprint(v)), true
}

func fromUint(u uint64) Value {
	if u <= math.MaxInt64 {
		return Int(int64(u))
	}
	return Double(float64(u))
}

func fromFloat(f float64) Value {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return String(strconv.FormatFloat(f, 'g', -1, 64))
	}
	return Double(f)
}

// docs/12 §3 size cuts, in UTF-8 bytes.
const (
	BodyMaxBytes       = 16 * 1024
	StacktraceMaxBytes = 40 * 1024
	AttributeMaxBytes  = 4 * 1024
)

// CutUTF8 cuts s to at most max bytes without splitting a UTF-8 sequence.
// Invalid bytes are replaced first, so the result is always valid UTF-8.
func CutUTF8(s string, max int) string {
	if !utf8.ValidString(s) {
		s = toValidUTF8(s)
	}
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func toValidUTF8(s string) string {
	b := make([]byte, 0, len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		b = utf8.AppendRune(b, r) // invalid bytes decode as utf8.RuneError (U+FFFD)
		s = s[size:]
	}
	return string(b)
}

// Finalize applies the size cuts; a cut record carries logify.truncated = true.
// Invalid UTF-8 is replaced first; that replacement alone does not mark the
// record.
func Finalize(r *Record) {
	truncated := false
	body := r.Body
	if !utf8.ValidString(body) {
		body = toValidUTF8(body)
	}
	if len(body) > BodyMaxBytes {
		truncated = true
	}
	r.Body = CutUTF8(body, BodyMaxBytes)
	for i := range r.Attrs {
		if r.Attrs[i].Value.Kind != KindString {
			continue
		}
		max := AttributeMaxBytes
		if r.Attrs[i].Key == "exception.stacktrace" {
			max = StacktraceMaxBytes
		}
		valid := r.Attrs[i].Value.Str
		if !utf8.ValidString(valid) {
			valid = toValidUTF8(valid)
		}
		if len(valid) > max {
			truncated = true
		}
		r.Attrs[i].Value.Str = CutUTF8(valid, max)
	}
	if truncated {
		r.Attrs = Set(r.Attrs, "logify.truncated", Bool(true))
	}
}
