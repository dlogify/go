// Package run implements logify run (docs/12 §5.7): it runs a command with
// its output passed through unchanged, parses a copy (docs/12 §5) and sends
// the records.
package run

import (
	"os"
	"strings"
	"sync"
	"time"

	"dlogify.com/sdk/go/internal/bridge"
	"dlogify.com/sdk/go/internal/parse"
	"dlogify.com/sdk/go/internal/record"
)

const (
	usage     = "usage: logify run -- <command> [args...]\n"
	exitFlush = 5 * time.Second
	tickEvery = 100 * time.Millisecond
	// After the child exits, how long its pipes may still deliver output. A
	// background grandchild can hold them open forever; we do not wait for it.
	pipeGrace = 500 * time.Millisecond
	scope     = "logify.run"
)

func parseArgs(args []string) (string, []string, bool) {
	if len(args) < 3 || args[0] != "run" || args[1] != "--" {
		return "", nil, false
	}
	return args[2], args[3:], true
}

// Main runs logify with its arguments (without the program name) and returns
// the exit code. When the child died from a signal, Main may end the process
// with that signal instead of returning.
func Main(args []string) int {
	command, rest, ok := parseArgs(args)
	if !ok {
		os.Stderr.WriteString(usage)
		return 2
	}
	return runCommand(bridge.NewRunClient(), command, rest)
}

func childEnv(environ []string) []string {
	for _, kv := range environ {
		if strings.HasPrefix(kv, "PYTHONUNBUFFERED=") {
			return environ
		}
	}
	return append(environ, "PYTHONUNBUFFERED=1")
}

// lockedParser guards one stream's parser: the reader goroutine pushes, the
// ticker ticks.
type lockedParser struct {
	mu     sync.Mutex
	p      *parse.StreamParser
	client bridge.Client
}

func newParser(client bridge.Client, stream string) *lockedParser {
	emit := func(p parse.Parsed) {
		client.EmitRecord(record.Record{Scope: scope, Severity: p.Severity, Body: p.Body, Time: p.Time, Attrs: p.Attrs})
	}
	return &lockedParser{p: parse.NewStreamParser(stream, time.Now, emit), client: client}
}

func (l *lockedParser) do(fn func(*parse.StreamParser)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			l.client.Debugf("parse failed: %v", r)
		}
	}()
	fn(l.p)
}

func (l *lockedParser) push(chunk []byte) { l.do(func(p *parse.StreamParser) { p.Push(chunk) }) }
func (l *lockedParser) tick()             { l.do(func(p *parse.StreamParser) { p.Tick() }) }
func (l *lockedParser) end()              { l.do(func(p *parse.StreamParser) { p.End() }) }
