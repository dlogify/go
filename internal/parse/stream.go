package parse

import (
	"time"

	"dlogify.com/sdk/go/internal/record"
)

// docs/12 §5: one parser per stream, each with its own open event. The clock
// is injected so tests replay pauses without sleeping. A StreamParser is not
// safe for concurrent use; logify run guards each one with a mutex.
const (
	IdleClose     = 500 * time.Millisecond
	MaxEventLines = 500
	MaxEventBytes = 40 * 1024
)

type openEvent struct {
	lines     []string
	bytes     int
	truncated bool
	startedAt time.Time
}

type StreamParser struct {
	stream     string
	now        func() time.Time
	emit       func(Parsed)
	splitter   LineSplitter
	event      *openEvent
	lastLineAt time.Time
}

func NewStreamParser(stream string, now func() time.Time, emit func(Parsed)) *StreamParser {
	return &StreamParser{stream: stream, now: now, emit: emit}
}

func (p *StreamParser) Push(chunk []byte) {
	for _, l := range p.splitter.Push(chunk) {
		p.line(l)
	}
}

// Tick closes the open event after IdleClose without a new line.
func (p *StreamParser) Tick() {
	if p.event != nil && p.now().Sub(p.lastLineAt) >= IdleClose {
		p.close()
	}
}

func (p *StreamParser) End() {
	for _, l := range p.splitter.End() {
		p.line(l)
	}
	p.close()
}

func (p *StreamParser) line(l Line) {
	now := p.now()
	p.lastLineAt = now
	text := StripANSI(l.Text)
	if rec, ok := ParseJSONLine(text); ok {
		p.close()
		if l.Truncated {
			rec.Attrs = record.Set(rec.Attrs, "logify.truncated", record.Bool(true))
		}
		p.send(rec, now)
		return
	}
	if p.event != nil && continuesEvent(p.event.lines, text) {
		p.append(text, l.Truncated)
		return
	}
	p.close()
	if isBlank(text) {
		return
	}
	p.event = &openEvent{startedAt: now}
	p.append(text, l.Truncated)
}

func (p *StreamParser) append(text string, truncated bool) {
	e := p.event
	e.lines = append(e.lines, text)
	e.bytes += len(text) + 1
	e.truncated = e.truncated || truncated
	if len(e.lines) >= MaxEventLines || e.bytes >= MaxEventBytes {
		e.truncated = true
		p.close()
	}
}

func (p *StreamParser) close() {
	e := p.event
	if e == nil {
		return
	}
	p.event = nil
	if rec, ok := buildTextRecord(e.lines, e.truncated); ok {
		p.send(rec, e.startedAt)
	}
}

func (p *StreamParser) send(rec Parsed, arrived time.Time) {
	attrs := []record.Attr{{Key: "log.iostream", Value: record.String(p.stream)}}
	for _, a := range rec.Attrs {
		attrs = record.Set(attrs, a.Key, a.Value)
	}
	rec.Attrs = attrs
	if rec.Time == 0 {
		rec.Time = arrived.UnixNano()
	}
	p.emit(rec)
}
