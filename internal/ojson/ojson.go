// Package ojson parses JSON keeping each object's key order and writes it back
// compact, the way JavaScript's JSON.stringify does. docs/12 §5.2 asks for
// nested values re-serialized with their keys in the original order, and the
// TypeScript client defines the exact text.
package ojson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

type Kind uint8

const (
	Null Kind = iota
	Bool
	Number
	String
	Array
	Object
)

type Member struct {
	Key   string
	Value Value
}

type Value struct {
	Kind Kind
	Bool bool
	Num  json.Number
	Str  string
	Arr  []Value
	Obj  []Member
}

var errTrailing = errors.New("ojson: data after the JSON value")

// Parse reads exactly one JSON value; only whitespace may follow it.
func Parse(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return Value{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Value{}, errTrailing
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return Value{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := []Member{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return Value{}, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return Value{}, errors.New("ojson: object key is not a string")
				}
				v, err := parseValue(dec)
				if err != nil {
					return Value{}, err
				}
				obj = setMember(obj, key, v)
			}
			if _, err := dec.Token(); err != nil {
				return Value{}, err
			}
			return Value{Kind: Object, Obj: obj}, nil
		case '[':
			arr := []Value{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return Value{}, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return Value{}, err
			}
			return Value{Kind: Array, Arr: arr}, nil
		}
		return Value{}, errors.New("ojson: unexpected delimiter")
	case string:
		return Value{Kind: String, Str: t}, nil
	case json.Number:
		return Value{Kind: Number, Num: t}, nil
	case bool:
		return Value{Kind: Bool, Bool: t}, nil
	case nil:
		return Value{Kind: Null}, nil
	}
	return Value{}, errors.New("ojson: unexpected token")
}

// Like JSON.parse: a duplicate key keeps its first position and its last value.
func setMember(obj []Member, key string, v Value) []Member {
	for i := range obj {
		if obj[i].Key == key {
			obj[i].Value = v
			return obj
		}
	}
	return append(obj, Member{Key: key, Value: v})
}

func (v Value) Get(key string) (Value, bool) {
	for _, m := range v.Obj {
		if m.Key == key {
			return m.Value, true
		}
	}
	return Value{}, false
}

// Float returns the number's value as JavaScript sees it (±Inf when it
// overflows, 0 when it underflows).
func (v Value) Float() float64 {
	f, _ := strconv.ParseFloat(string(v.Num), 64)
	return f
}

func (v Value) Compact() string {
	return string(v.appendTo(nil))
}

func (v Value) appendTo(b []byte) []byte {
	switch v.Kind {
	case Null:
		return append(b, "null"...)
	case Bool:
		return strconv.AppendBool(b, v.Bool)
	case Number:
		return append(b, FormatNumber(v.Float())...)
	case String:
		return AppendString(b, v.Str)
	case Array:
		b = append(b, '[')
		for i, item := range v.Arr {
			if i > 0 {
				b = append(b, ',')
			}
			b = item.appendTo(b)
		}
		return append(b, ']')
	default:
		b = append(b, '{')
		for i, m := range v.Obj {
			if i > 0 {
				b = append(b, ',')
			}
			b = AppendString(b, m.Key)
			b = append(b, ':')
			b = m.Value.appendTo(b)
		}
		return append(b, '}')
	}
}

// FormatNumber writes f as JavaScript's Number#toString does (encoding/json
// follows the same rules for float64); non-finite values become null, as in
// JSON.stringify.
func FormatNumber(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "null"
	}
	if f == 0 {
		return "0"
	}
	b, _ := json.Marshal(f)
	return string(b)
}

const hexDigits = "0123456789abcdef"

// AppendString writes s as a JSON string the way JSON.stringify does: only
// the quote, the backslash and control characters are escaped; invalid UTF-8
// becomes U+FFFD.
func AppendString(b []byte, s string) []byte {
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"':
				b = append(b, '\\', '"')
			case '\\':
				b = append(b, '\\', '\\')
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				if c < 0x20 {
					b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
				} else {
					b = append(b, c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, "\uFFFD"...)
		} else {
			b = append(b, s[i:i+size]...)
		}
		i += size
	}
	return append(b, '"')
}
