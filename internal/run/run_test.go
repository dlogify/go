//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package run_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var logifyBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "logify-run")
	if err != nil {
		panic(err)
	}
	logifyBin = filepath.Join(dir, "logify")
	build := exec.Command("go", "build", "-o", logifyBin, "dlogify.com/sdk/go/cmd/logify")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type sent struct {
	Body     string
	Severity int
	Scope    string
	Attrs    map[string]any
}

type ingest struct {
	*httptest.Server
	mu      sync.Mutex
	records []sent
	agents  []string
}

func newIngest(t *testing.T) *ingest {
	s := &ingest{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, _ := gzip.NewReader(r.Body)
		raw, _ := io.ReadAll(zr)
		var body struct {
			ResourceLogs []struct {
				ScopeLogs []struct {
					Scope      struct{ Name string }
					LogRecords []struct {
						SeverityNumber int
						Body           struct{ StringValue string }
						Attributes     []struct {
							Key   string
							Value map[string]any
						}
					}
				}
			}
		}
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.agents = append(s.agents, r.Header.Get("User-Agent"))
		for _, rl := range body.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					attrs := map[string]any{}
					for _, a := range lr.Attributes {
						for _, v := range a.Value {
							attrs[a.Key] = v
						}
					}
					s.records = append(s.records, sent{Body: lr.Body.StringValue, Severity: lr.SeverityNumber, Scope: sl.Scope.Name, Attrs: attrs})
				}
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *ingest) all() []sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sent(nil), s.records...)
}

func (s *ingest) userAgents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.agents...)
}

// The parent's environment without PYTHONUNBUFFERED, plus the key and the
// fake endpoint.
func testEnv(s *ingest, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PYTHONUNBUFFERED=") {
			env = append(env, kv)
		}
	}
	return append(append(env, "LOGIFY_API_KEY=lgf_test", "LOGIFY_ENDPOINT="+s.URL), extra...)
}

func logifyCmd(s *ingest, args ...string) *exec.Cmd {
	cmd := exec.Command(logifyBin, args...)
	cmd.Env = testEnv(s)
	return cmd
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func TestUsage(t *testing.T) {
	s := newIngest(t)
	for _, args := range [][]string{nil, {"run"}, {"run", "--"}, {"run", "ls"}} {
		var stderr bytes.Buffer
		cmd := logifyCmd(s, args...)
		cmd.Stderr = &stderr
		if code := exitCode(cmd.Run()); code != 2 || !strings.HasPrefix(stderr.String(), "usage: logify run -- ") {
			t.Fatalf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}

func TestPassthroughAndRecords(t *testing.T) {
	s := newIngest(t)
	var stdout, stderr bytes.Buffer
	cmd := logifyCmd(s, "run", "--", "sh", "-c", `printf 'hello\n'; printf 'ERROR boom\n' >&2; printf 'a\000b\377'; exit 3`)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if code := exitCode(cmd.Run()); code != 3 {
		t.Fatalf("exit code %d", code)
	}
	if stdout.String() != "hello\na\x00b\xff" || stderr.String() != "ERROR boom\n" {
		t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	byBody := map[string]sent{}
	for _, r := range s.all() {
		byBody[r.Body] = r
	}
	if r := byBody["hello"]; r.Scope != "logify.run" || r.Severity != 0 || r.Attrs["log.iostream"] != "stdout" {
		t.Fatalf("hello: %+v", r)
	}
	if r := byBody["ERROR boom"]; r.Severity != 17 || r.Attrs["log.iostream"] != "stderr" {
		t.Fatalf("ERROR boom: %+v", r)
	}
	if r := byBody["Process exited with code 3"]; r.Severity != 17 || r.Attrs["process.exit_code"] != "3" {
		t.Fatalf("exit record: %+v", r)
	}
	if agents := s.userAgents(); len(agents) == 0 || !strings.HasSuffix(agents[0], " run") || !strings.HasPrefix(agents[0], "logify-go/") {
		t.Fatalf("user agents %v", agents)
	}
}

func TestCommandThatCannotStart(t *testing.T) {
	s := newIngest(t)
	notExec := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command string
		code    int
		reason  string
	}{
		{"does-not-exist-xyz", 127, "command not found"},
		{notExec, 126, "permission denied"},
	}
	for _, c := range cases {
		var stderr bytes.Buffer
		cmd := logifyCmd(s, "run", "--", c.command)
		cmd.Stderr = &stderr
		if code := exitCode(cmd.Run()); code != c.code || stderr.String() != "logify: cannot run "+c.command+": "+c.reason+"\n" {
			t.Fatalf("%s: code %d, stderr %q", c.command, code, stderr.String())
		}
	}
}

func TestSigtermIsForwarded(t *testing.T) {
	s := newIngest(t)
	cmd := logifyCmd(s, "run", "--", "sh", "-c", `trap 'echo got-term; exit 0' TERM; echo ready; while :; do sleep 0.05; done`)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(pipe)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("first line %q, %v", line, err)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	rest, _ := io.ReadAll(reader)
	if code := exitCode(cmd.Wait()); code != 0 || string(rest) != "got-term\n" {
		t.Fatalf("code %d, rest %q", code, rest)
	}
	for _, r := range s.all() {
		if strings.HasPrefix(r.Body, "Process ") {
			t.Fatalf("unexpected exit record %+v", r)
		}
	}
}

func TestChildKilledBySignalIsReportedAndReraised(t *testing.T) {
	s := newIngest(t)
	cmd := logifyCmd(s, "run", "--", "sh", "-c", `kill -TERM $$`)
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("err %v", err)
	}
	status := ee.Sys().(syscall.WaitStatus)
	if !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("logify run must die from SIGTERM too: %v", status)
	}
	found := false
	for _, r := range s.all() {
		if r.Body == "Process terminated by SIGTERM" && r.Attrs["process.signal"] == "SIGTERM" && r.Severity == 17 {
			found = true
		}
	}
	if !found {
		t.Fatalf("records %+v", s.all())
	}
}

func TestPipelineSigpipeIsNotReported(t *testing.T) {
	s := newIngest(t)
	cmd := exec.Command("sh", "-c", `"$LOGIFY_BIN" run -- yes | head -n 1`)
	cmd.Env = testEnv(s, "LOGIFY_BIN="+logifyBin)
	out, err := cmd.Output()
	if err != nil || string(out) != "y\n" {
		t.Fatalf("err %v, out %q", err, out)
	}
	time.Sleep(200 * time.Millisecond)
	for _, r := range s.all() {
		if strings.HasPrefix(r.Body, "Process ") {
			t.Fatalf("a pipeline that ended must not be reported: %+v", r)
		}
	}
}

func TestPythonUnbufferedIsSet(t *testing.T) {
	s := newIngest(t)
	cmd := logifyCmd(s, "run", "--", "sh", "-c", `printf '%s' "$PYTHONUNBUFFERED"`)
	out, err := cmd.Output()
	if err != nil || string(out) != "1" {
		t.Fatalf("err %v, out %q", err, out)
	}
}
