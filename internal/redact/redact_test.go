package redact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"dlogify.com/sdk/go/internal/conformance"
)

func TestEngineCases(t *testing.T) {
	// From services/engine/src/pipeline/redact.rs tests.
	cases := map[string]string{
		"Authorization: Bearer abcdef0123456789":                "Authorization: Bearer <token>",
		"aws AKIAIOSFODNN7EXAMPLE ok":                           "aws <secret> ok",
		"aws AKIAIOSFODNN7EXAMPLEX ok":                          "aws AKIAIOSFODNN7EXAMPLEX ok",
		"card 4111 1111 1111 1111 1x":                           "card <card> 1x",
		"x@a.bc.d1":                                             "<email>.d1",
		"link fe80::1ff:fe23:4567:890a up":                      "link <ip> up",
		"failed in db::add":                                     "failed in db::add",
		"version 1.2.3 and 300.1.2.3":                           "version 1.2.3 and 300.1.2.3",
		"redis://:hunter2@localhost:6379":                       "redis://:<secret>@localhost:6379",
		"token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghij": "token=<secret>",
		"charge 4111111111111111 12/28 declined":                "charge <card> 12/28 declined",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestSensitiveKeys(t *testing.T) {
	for _, k := range []string{"Authorization", "session_id", "X-Api-Key", "db.password", "auth_token_set"} {
		if !IsSensitiveKey(k) {
			t.Errorf("%s should be sensitive", k)
		}
	}
	if IsSensitiveKey("order_id") {
		t.Error("order_id is not sensitive")
	}
}

type fixture struct {
	Options map[string]any `json:"options"`
	Cases   []struct {
		ClientOnly bool `json:"client_only"`
		Input      struct {
			Body       string         `json:"body"`
			Attributes map[string]any `json:"attributes"`
		} `json:"input"`
		Expected struct {
			Body       string         `json:"body"`
			Attributes map[string]any `json:"attributes"`
		} `json:"expected"`
	} `json:"cases"`
}

// Files whose options are exactly {"redact_defaults": true}: the built-in
// rules alone. The client-level rules run in the root package's conformance
// test (Task 9).
func TestBuiltinConformance(t *testing.T) {
	dir := conformance.Dir(t, "redaction")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	ran := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		if len(f.Options) != 1 || f.Options["redact_defaults"] != true {
			continue
		}
		for i, c := range f.Cases {
			if got := Text(c.Input.Body); got != c.Expected.Body {
				t.Errorf("%s case %d body:\n got %q\nwant %q", filepath.Base(file), i, got, c.Expected.Body)
			}
			for k, v := range c.Input.Attributes {
				want := c.Expected.Attributes[k]
				var got any = v
				if IsSensitiveKey(k) {
					got = Secret
				} else if s, ok := v.(string); ok {
					got = Text(s)
				}
				if got != want {
					t.Errorf("%s case %d attribute %s: got %#v, want %#v", filepath.Base(file), i, k, got, want)
				}
			}
			ran++
		}
	}
	if ran == 0 {
		t.Fatal("no built-in cases ran")
	}
}

func FuzzText(f *testing.F) {
	for _, s := range []string{"bearer abcdefghij", "a@b.co", "4111 1111 1111 1111", "::1", "é192.168.1.1", "password=x\ufeffy"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Text(s)
		if utf8.ValidString(s) && !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 out of %q", s)
		}
	})
}

func TestPlaceholdersAreStable(t *testing.T) {
	for _, p := range []string{"<secret>", "<email>", "<card>", "<ip>", "<jwt>", "<token>", "<redacted>"} {
		if Text(p) != p {
			t.Fatalf("placeholder %s changed", p)
		}
	}
}
