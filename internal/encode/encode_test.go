package encode

import (
	"encoding/json"
	"testing"

	"dlogify.com/sdk/go/internal/record"
)

func TestRecordEncoding(t *testing.T) {
	r := &record.Record{
		Scope: "logify", Time: 1790000000123000000, Observed: 1790000000124000000,
		Severity: record.SeverityError, Body: "charge failed",
		Attrs: []record.Attr{
			{Key: "s", Value: record.String("x")}, {Key: "i", Value: record.Int(3)},
			{Key: "d", Value: record.Double(0.5)}, {Key: "b", Value: record.Bool(false)},
		},
	}
	got := string(Record(r).JSON)
	want := `{"timeUnixNano":"1790000000123000000","observedTimeUnixNano":"1790000000124000000","severityNumber":17,"severityText":"ERROR","body":{"stringValue":"charge failed"},"attributes":[{"key":"s","value":{"stringValue":"x"}},{"key":"i","value":{"intValue":"3"}},{"key":"d","value":{"doubleValue":0.5}},{"key":"b","value":{"boolValue":false}}]}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestUnknownLevelOmitsSeverityAndEmptyAttributesIsAnArray(t *testing.T) {
	got := string(Record(&record.Record{Scope: "logify.run", Time: 1, Observed: 2, Body: "x"}).JSON)
	want := `{"timeUnixNano":"1","observedTimeUnixNano":"2","body":{"stringValue":"x"},"attributes":[]}`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestRequestGroupsScopesInOrderOfFirstAppearance(t *testing.T) {
	a := Record(&record.Record{Scope: "a", Body: "1"})
	b := Record(&record.Record{Scope: "b", Body: "2"})
	c := Record(&record.Record{Scope: "a", Body: "3"})
	var out struct {
		ResourceLogs []struct {
			ScopeLogs []struct {
				Scope      struct{ Name string }
				LogRecords []json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(Request(nil, []Encoded{a, b, c}), &out); err != nil {
		t.Fatal(err)
	}
	scopes := out.ResourceLogs[0].ScopeLogs
	if len(scopes) != 2 || scopes[0].Scope.Name != "a" || len(scopes[0].LogRecords) != 2 || scopes[1].Scope.Name != "b" {
		t.Fatalf("got %+v", scopes)
	}
}

func TestResourceFixedAttributesWin(t *testing.T) {
	attrs := Resource(ResourceConfig{Service: "svc", Environment: "prod", Attributes: []record.Attr{
		{Key: "service.name", Value: record.String("x")}, {Key: "team", Value: record.String("t")},
	}})
	keys := []string{}
	for _, a := range attrs {
		keys = append(keys, a.Key)
	}
	want := []string{"service.name", "deployment.environment.name", "host.name", "telemetry.sdk.name", "telemetry.sdk.language", "telemetry.sdk.version", "team"}
	if len(keys) != len(want) {
		t.Fatalf("got %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("got %v", keys)
		}
	}
	if attrs[0].Value.Str != "svc" {
		t.Fatal("service.name replaced")
	}
}
