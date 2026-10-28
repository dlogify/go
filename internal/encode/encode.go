// Package encode writes records as OTLP/JSON (docs/12 §2–3). Each record is
// encoded once, when it is queued; a request is the concatenation of its
// records' fragments under one ResourceLogs.
package encode

import (
	"os"
	"strconv"

	"dlogify.com/sdk/go/internal/ojson"
	"dlogify.com/sdk/go/internal/record"
	"dlogify.com/sdk/go/internal/version"
)

type Encoded struct {
	Scope string
	JSON  []byte
}

func Record(r *record.Record) Encoded {
	b := make([]byte, 0, 256+len(r.Body))
	b = append(b, `{"timeUnixNano":"`...)
	b = strconv.AppendInt(b, r.Time, 10)
	b = append(b, `","observedTimeUnixNano":"`...)
	b = strconv.AppendInt(b, r.Observed, 10)
	b = append(b, '"')
	if text := record.SeverityText(r.Severity); text != "" {
		b = append(b, `,"severityNumber":`...)
		b = strconv.AppendInt(b, int64(r.Severity), 10)
		b = append(b, `,"severityText":"`...)
		b = append(b, text...)
		b = append(b, '"')
	}
	b = append(b, `,"body":{"stringValue":`...)
	b = ojson.AppendString(b, r.Body)
	b = append(b, `},"attributes":`...)
	b = appendAttrs(b, r.Attrs)
	b = append(b, '}')
	return Encoded{Scope: r.Scope, JSON: b}
}

func appendAttrs(b []byte, attrs []record.Attr) []byte {
	b = append(b, '[')
	for i, a := range attrs {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"key":`...)
		b = ojson.AppendString(b, a.Key)
		b = append(b, `,"value":`...)
		b = appendValue(b, a.Value)
		b = append(b, '}')
	}
	return append(b, ']')
}

func appendValue(b []byte, v record.Value) []byte {
	switch v.Kind {
	case record.KindInt:
		b = append(b, `{"intValue":"`...)
		b = strconv.AppendInt(b, v.Int, 10)
		return append(b, `"}`...)
	case record.KindDouble:
		b = append(b, `{"doubleValue":`...)
		b = append(b, ojson.FormatNumber(v.Float)...)
		return append(b, '}')
	case record.KindBool:
		b = append(b, `{"boolValue":`...)
		b = strconv.AppendBool(b, v.Bool)
		return append(b, '}')
	default:
		b = append(b, `{"stringValue":`...)
		b = ojson.AppendString(b, v.Str)
		return append(b, '}')
	}
}

// Request builds one ResourceLogs with one ScopeLogs per scope, in order of
// first appearance.
func Request(resource []record.Attr, records []Encoded) []byte {
	var order []string
	byScope := map[string][]int{}
	for i, r := range records {
		if _, ok := byScope[r.Scope]; !ok {
			order = append(order, r.Scope)
		}
		byScope[r.Scope] = append(byScope[r.Scope], i)
	}
	size := 128
	for _, r := range records {
		size += len(r.JSON) + 1
	}
	b := make([]byte, 0, size)
	b = append(b, `{"resourceLogs":[{"resource":{"attributes":`...)
	b = appendAttrs(b, resource)
	b = append(b, `},"scopeLogs":[`...)
	for i, scope := range order {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"scope":{"name":`...)
		b = ojson.AppendString(b, scope)
		b = append(b, `},"logRecords":[`...)
		for j, idx := range byScope[scope] {
			if j > 0 {
				b = append(b, ',')
			}
			b = append(b, records[idx].JSON...)
		}
		b = append(b, `]}`...)
	}
	return append(b, `]}]}`...)
}

type ResourceConfig struct {
	Service, Environment, Release string
	Attributes                    []record.Attr
}

// Resource returns the fixed resource attributes, then the configured ones
// that do not replace a fixed key.
func Resource(cfg ResourceConfig) []record.Attr {
	attrs := []record.Attr{
		{Key: "service.name", Value: record.String(cfg.Service)},
		{Key: "deployment.environment.name", Value: record.String(cfg.Environment)},
	}
	if cfg.Release != "" {
		attrs = append(attrs, record.Attr{Key: "service.version", Value: record.String(cfg.Release)})
	}
	attrs = append(attrs,
		record.Attr{Key: "host.name", Value: record.String(HostName())},
		record.Attr{Key: "telemetry.sdk.name", Value: record.String(version.SDKName)},
		record.Attr{Key: "telemetry.sdk.language", Value: record.String(version.SDKLanguage)},
		record.Attr{Key: "telemetry.sdk.version", Value: record.String(version.Version)},
	)
	fixed := len(attrs)
	for _, a := range cfg.Attributes {
		taken := false
		for _, f := range attrs[:fixed] {
			if f.Key == a.Key {
				taken = true
				break
			}
		}
		if !taken {
			attrs = record.Set(attrs, a.Key, a.Value)
		}
	}
	return attrs
}

func HostName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}
