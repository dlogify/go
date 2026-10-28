package logify

import (
	"log/slog"
	"time"

	"dlogify.com/sdk/go/internal/record"
)

// flattenAttrs converts slog attributes to record attributes: groups become
// dotted keys, LogValuers are resolved, attributes with an empty key are
// skipped, and the first value that is an error is returned instead of being
// added (it becomes exception.*).
func flattenAttrs(attrs []slog.Attr) ([]record.Attr, error) {
	var out []record.Attr
	var first error
	var walk func(prefix string, attrs []slog.Attr)
	walk = func(prefix string, attrs []slog.Attr) {
		for _, a := range attrs {
			v := a.Value.Resolve()
			if v.Kind() == slog.KindGroup {
				group := v.Group()
				if len(group) == 0 {
					continue
				}
				p := prefix
				if a.Key != "" {
					p = prefix + a.Key + "."
				}
				walk(p, group)
				continue
			}
			if a.Key == "" {
				continue
			}
			if v.Kind() == slog.KindAny {
				if err, ok := v.Any().(error); ok && first == nil {
					first = err
					continue
				}
			}
			if rv, ok := slogValue(v); ok {
				out = record.Set(out, prefix+a.Key, rv)
			}
		}
	}
	walk("", attrs)
	return out, first
}

func slogValue(v slog.Value) (record.Value, bool) {
	switch v.Kind() {
	case slog.KindString:
		return record.String(v.String()), true
	case slog.KindInt64:
		return record.Int(v.Int64()), true
	case slog.KindUint64:
		return record.Normalize(v.Uint64())
	case slog.KindFloat64:
		return record.Normalize(v.Float64())
	case slog.KindBool:
		return record.Bool(v.Bool()), true
	case slog.KindDuration:
		return record.Int(int64(v.Duration())), true
	case slog.KindTime:
		return record.String(v.Time().Format(time.RFC3339Nano)), true
	}
	return record.Normalize(v.Any())
}
