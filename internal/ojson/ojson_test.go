package ojson

import "testing"

func TestCompactKeepsKeyOrderAndJavaScriptFormatting(t *testing.T) {
	cases := map[string]string{
		`{"b":1,"a":{"z":true,"y":null},"10":"x"}`:                      `{"b":1,"a":{"z":true,"y":null},"10":"x"}`,
		`{ "n": 1.0, "m": 1e2, "e": 1.5e-7, "big": 1e400, "neg0": -0 }`: `{"n":1,"m":100,"e":1.5e-7,"big":null,"neg0":0}`,
		`["<&>", "\u2028", "\u001b", "tab\t"]`:                          `["<&>","` + "\u2028" + `","\u001b","tab\t"]`,
		`{"dup":1,"other":2,"dup":3}`:                                   `{"dup":3,"other":2}`,
	}
	for in, want := range cases {
		v, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := v.Compact(); got != want {
			t.Fatalf("%s:\n got %s\nwant %s", in, got, want)
		}
	}
}

func TestParseRejectsInvalidAndTrailingData(t *testing.T) {
	for _, in := range []string{`{"a":1} x`, `{"a":}`, `{"a":1`, `{} {}`, ``} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Fatalf("%q: expected an error", in)
		}
	}
	if _, err := Parse([]byte("  {\"a\":1}  \n")); err != nil {
		t.Fatal(err)
	}
}

func TestGet(t *testing.T) {
	v, _ := Parse([]byte(`{"a":"x","b":2}`))
	if got, ok := v.Get("a"); !ok || got.Kind != String || got.Str != "x" {
		t.Fatalf("got %#v", got)
	}
	if _, ok := v.Get("c"); ok {
		t.Fatal("unexpected key")
	}
}

func TestFormatNumber(t *testing.T) {
	for f, want := range map[float64]string{1: "1", 0.5: "0.5", 1e21: "1e+21", 123456789012: "123456789012"} {
		if got := FormatNumber(f); got != want {
			t.Fatalf("%v: %s", f, got)
		}
	}
}
