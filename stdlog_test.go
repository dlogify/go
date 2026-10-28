package logify_test

import (
	"bytes"
	"log"
	"strings"
	"testing"

	logify "dlogify.com/sdk/go"
)

func TestCaptureStdLog(t *testing.T) {
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	}()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("app: ")

	s := newIngest(t)
	c := newClient(t, s, logify.Options{})
	c.CaptureStdLog()
	c.CaptureStdLog() // a second call changes nothing
	log.Printf("ERROR: db down %d", 1)
	log.Print("plain line")
	func() {
		defer func() { _ = recover() }()
		log.Panic("bad state")
	}()

	if strings.Count(buf.String(), "app: ") != 3 {
		t.Fatalf("the original output must be kept:\n%s", buf.String())
	}
	got := flushed(t, c, s)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	want := []struct {
		body     string
		severity int
	}{{"ERROR: db down 1", 17}, {"plain line", 9}, {"bad state", 21}}
	for i, w := range want {
		if got[i].Scope != "log" || got[i].Body != w.body || got[i].Severity != w.severity {
			t.Fatalf("record %d: %+v", i, got[i])
		}
	}
	c.Shutdown(0)
	if log.Writer() != &buf {
		t.Fatal("Shutdown must restore the log output")
	}
}
