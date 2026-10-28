package diag

import (
	"bytes"
	"testing"
)

func TestOutput(t *testing.T) {
	var buf bytes.Buffer
	defer SetOutput(&buf)()
	d := New(false)
	d.Warn("a %d", 1)
	d.WarnOnce("k", "once")
	d.WarnOnce("k", "once")
	d.Debug("hidden")
	New(true).Debug("shown")
	var nilDiag *Diag
	nilDiag.Warn("no panic")
	want := "logify: warning: a 1\nlogify: warning: once\nlogify: shown\n"
	if buf.String() != want {
		t.Fatalf("got %q", buf.String())
	}
}

func TestIsFlag(t *testing.T) {
	for v, want := range map[string]bool{"1": true, " TRUE ": true, "yes": false, "": false, "0": false} {
		if IsFlag(v) != want {
			t.Fatalf("%q", v)
		}
	}
}
