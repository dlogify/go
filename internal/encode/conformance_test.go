package encode_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dlogify.com/sdk/go/internal/conformance"
	"dlogify.com/sdk/go/internal/encode"
	"dlogify.com/sdk/go/internal/ojson"
	"dlogify.com/sdk/go/internal/record"
	"dlogify.com/sdk/go/internal/version"
)

// ToValue maps a fixture value to an attribute value as the SDK would build it.
func toValue(v ojson.Value) (record.Value, bool) {
	switch v.Kind {
	case ojson.String:
		return record.String(v.Str), true
	case ojson.Bool:
		return record.Bool(v.Bool), true
	case ojson.Number:
		f := v.Float()
		if f == math.Trunc(f) && math.Abs(f) <= 1<<53 {
			return record.Int(int64(f)), true
		}
		return record.Double(f), true
	case ojson.Null:
		return record.Value{}, false
	}
	return record.String(v.Compact()), true
}

func attrs(v ojson.Value) []record.Attr {
	var out []record.Attr
	for _, m := range v.Obj {
		if rv, ok := toValue(m.Value); ok {
			out = append(out, record.Attr{Key: m.Key, Value: rv})
		}
	}
	return out
}

var severity = map[string]int{"trace": 1, "debug": 5, "info": 9, "warn": 13, "error": 17, "fatal": 21}

func replacePlaceholders(v any) any {
	switch x := v.(type) {
	case string:
		switch x {
		case "$HOST_NAME":
			return encode.HostName()
		case "$SDK_NAME":
			return version.SDKName
		case "$SDK_LANGUAGE":
			return version.SDKLanguage
		case "$SDK_VERSION":
			return version.Version
		}
		return x
	case []any:
		for i := range x {
			x[i] = replacePlaceholders(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = replacePlaceholders(x[k])
		}
	}
	return v
}

func TestOTLPConformance(t *testing.T) {
	dir := conformance.Dir(t, "otlp")
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
			fixture, err := ojson.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			config, _ := fixture.Get("config")
			str := func(v ojson.Value, key string) string { x, _ := v.Get(key); return x.Str }
			cfgAttrs, _ := config.Get("attributes")
			resource := encode.Resource(encode.ResourceConfig{
				Service: str(config, "service"), Environment: str(config, "environment"), Release: str(config, "release"),
				Attributes: attrs(cfgAttrs),
			})
			recordsValue, _ := fixture.Get("records")
			var encoded []encode.Encoded
			for _, r := range recordsValue.Arr {
				level, _ := r.Get("level")
				rec := &record.Record{Scope: str(r, "scope"), Body: str(r, "body")}
				if level.Kind == ojson.String {
					rec.Severity = severity[level.Str]
				}
				var timeNs, observedNs int64
				json.Unmarshal([]byte(str(r, "time_unix_nano")), &timeNs)
				json.Unmarshal([]byte(str(r, "observed_time_unix_nano")), &observedNs)
				rec.Time, rec.Observed = timeNs, observedNs
				a, _ := r.Get("attributes")
				rec.Attrs = attrs(a)
				encoded = append(encoded, encode.Record(rec))
			}
			var got, want any
			if err := json.Unmarshal(encode.Request(resource, encoded), &got); err != nil {
				t.Fatal(err)
			}
			var whole map[string]any
			if err := json.Unmarshal(data, &whole); err != nil {
				t.Fatal(err)
			}
			want = replacePlaceholders(whole["expected_body"])
			if !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", "  ")
				w, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("got:\n%s\nwant:\n%s", g, w)
			}
		})
	}
}
