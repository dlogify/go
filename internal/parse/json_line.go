package parse

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"dlogify.com/sdk/go/internal/ojson"
	"dlogify.com/sdk/go/internal/record"
)

// docs/12 §5.2.
var (
	bodyKeys  = []string{"msg", "message", "@message", "event"}
	levelKeys = []string{"level", "severity", "levelname", "@level", "log.level"}
	timeKeys  = []string{"time", "timestamp", "@timestamp", "ts"}
	errorKeys = []string{"err", "error", "exception"}
	isoTime   = regexp.MustCompile(`(?i)^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?)(?:[.,](\d+))?(Z|[+-]\d{2}(?::?\d{2})?)?$`)
)

const maxSafeInteger = 1 << 53

// ParseJSONLine returns the record of a line that is one JSON object.
func ParseJSONLine(line string) (Parsed, bool) {
	if !strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "{") {
		return Parsed{}, false
	}
	v, err := ojson.Parse([]byte(line))
	if err != nil || v.Kind != ojson.Object {
		return Parsed{}, false
	}
	first := func(keys []string, accept func(ojson.Value) bool) (string, ojson.Value, bool) {
		for _, k := range keys {
			if x, ok := v.Get(k); ok && x.Kind != ojson.Null && accept(x) {
				return k, x, true
			}
		}
		return "", ojson.Value{}, false
	}
	any := func(ojson.Value) bool { return true }
	used := map[string]bool{}
	bodyKey, body, hasBody := first(bodyKeys, func(x ojson.Value) bool { return x.Kind == ojson.String })
	levelKey, level, hasLevel := first(levelKeys, any)
	timeKey, tv, hasTime := first(timeKeys, any)
	errKey, ev, hasErr := first(errorKeys, any)
	var exc []record.Attr
	if hasErr {
		exc = errorAttributes(ev)
	}
	if hasBody {
		used[bodyKey] = true
	}
	if hasLevel {
		used[levelKey] = true
	}
	if hasTime {
		used[timeKey] = true
	}
	if hasErr && exc != nil {
		used[errKey] = true
	}

	p := Parsed{Attrs: exc}
	for _, m := range v.Obj {
		if used[m.Key] || m.Value.Kind == ojson.Null {
			continue
		}
		p.Attrs = record.Set(p.Attrs, m.Key, jsonValue(m.Value))
	}
	if hasBody {
		p.Body = body.Str
	}
	if hasLevel {
		p.Severity = jsonLevel(level)
	}
	if hasTime {
		if ns, ok := jsonTime(tv); ok {
			p.Time = ns
		}
	}
	return p, true
}

// Strings and booleans as they are; numbers as an int when they are an
// integer within ±2^53, else a double; anything else as its compact JSON.
func jsonValue(v ojson.Value) record.Value {
	switch v.Kind {
	case ojson.String:
		return record.String(v.Str)
	case ojson.Bool:
		return record.Bool(v.Bool)
	case ojson.Number:
		return numberValue(v.Float())
	}
	return record.String(v.Compact())
}

func numberValue(f float64) record.Value {
	switch {
	case math.IsInf(f, 1):
		return record.String("Infinity")
	case math.IsInf(f, -1):
		return record.String("-Infinity")
	case f == math.Trunc(f) && math.Abs(f) <= maxSafeInteger:
		return record.Int(int64(f))
	}
	return record.Double(f)
}

func jsonLevel(v ojson.Value) int {
	switch v.Kind {
	case ojson.String:
		n, _ := LevelFromWord(v.Str)
		return n
	case ojson.Number:
		n, _ := LevelFromNumber(v.Float())
		return n
	}
	return record.SeverityUnknown
}

func msToUnixNano(ms float64) int64 {
	return int64(math.Round(ms*1000)) * 1000
}

func jsonTime(v ojson.Value) (int64, bool) {
	switch v.Kind {
	case ojson.Number:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return 0, false
		}
		if f > 1e11 {
			return msToUnixNano(f), true
		}
		return msToUnixNano(f * 1000), true
	case ojson.String:
		return ParseISOTime(v.Str)
	}
	return 0, false
}

// ParseISOTime reads an ISO 8601 date and time; without an offset it is local
// time, as in JavaScript's Date.parse.
func ParseISOTime(text string) (int64, bool) {
	m := isoTime.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, false
	}
	base, fraction, zone := strings.Replace(m[1], " ", "T", 1), m[2], strings.ToUpper(m[3])
	layout := "2006-01-02T15:04:05"
	if len(base) == len("2006-01-02T15:04") {
		layout = "2006-01-02T15:04"
	}
	loc := time.Local
	switch {
	case zone == "Z":
		loc = time.UTC
	case zone != "":
		sign := 1
		if zone[0] == '-' {
			sign = -1
		}
		digits := strings.ReplaceAll(zone[1:], ":", "")
		hours, _ := strconv.Atoi(digits[:2])
		minutes := 0
		if len(digits) == 4 {
			minutes, _ = strconv.Atoi(digits[2:])
		}
		loc = time.FixedZone("", sign*(hours*3600+minutes*60))
	}
	t, err := time.ParseInLocation(layout, base, loc)
	if err != nil {
		return 0, false
	}
	frac := (fraction + "000000000")[:9]
	ns, _ := strconv.ParseInt(frac, 10, 64)
	return t.Unix()*1_000_000_000 + ns, true
}

func errorAttributes(v ojson.Value) []record.Attr {
	if v.Kind == ojson.String {
		exc, ok := ExtractException(strings.Split(strings.ReplaceAll(v.Str, "\r\n", "\n"), "\n"))
		if !ok {
			return nil
		}
		return []record.Attr{
			{Key: "exception.type", Value: record.String(exc.Type)},
			{Key: "exception.message", Value: record.String(exc.Message)},
			{Key: "exception.stacktrace", Value: record.String(exc.Stacktrace)},
		}
	}
	if v.Kind != ojson.Object {
		return nil
	}
	text := func(keys ...string) (string, bool) {
		for _, k := range keys {
			if x, ok := v.Get(k); ok && x.Kind == ojson.String {
				return x.Str, true
			}
		}
		return "", false
	}
	var out []record.Attr
	if s, ok := text("type", "name"); ok {
		out = append(out, record.Attr{Key: "exception.type", Value: record.String(s)})
	}
	if s, ok := text("message"); ok {
		out = append(out, record.Attr{Key: "exception.message", Value: record.String(s)})
	}
	if s, ok := text("stack", "stacktrace"); ok {
		out = append(out, record.Attr{Key: "exception.stacktrace", Value: record.String(s)})
	}
	return out
}
