package parse

import (
	"bytes"
	"regexp"
	"unicode/utf8"
)

// docs/12 §5.1: lines end at \n (a trailing \r is removed), are cut to 64 KB
// and decoded as UTF-8 with replacement characters.
const MaxLineBytes = 64 * 1024

// Kept beyond the limit so the cut can see the next byte (UTF-8 boundary).
const keepBytes = MaxLineBytes + 4

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func StripANSI(s string) string {
	if !bytes.ContainsRune([]byte(s), 0x1b) {
		return s
	}
	return ansi.ReplaceAllString(s, "")
}

type Line struct {
	Text      string
	Truncated bool
}

type LineSplitter struct {
	buf   []byte
	total int
}

func (s *LineSplitter) Push(chunk []byte) []Line {
	var lines []Line
	for {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			s.append(chunk)
			return lines
		}
		s.append(chunk[:i])
		lines = append(lines, s.take())
		chunk = chunk[i+1:]
	}
}

func (s *LineSplitter) End() []Line {
	if s.total == 0 {
		return nil
	}
	return []Line{s.take()}
}

func (s *LineSplitter) append(b []byte) {
	s.total += len(b)
	room := keepBytes - len(s.buf)
	if room <= 0 || len(b) == 0 {
		return
	}
	if len(b) > room {
		b = b[:room]
	}
	s.buf = append(s.buf, b...)
}

func (s *LineSplitter) take() Line {
	buf := s.buf
	truncated := s.total > MaxLineBytes
	s.buf = nil
	s.total = 0
	if truncated {
		end := MaxLineBytes
		for end > 0 && end > MaxLineBytes-3 && !utf8.RuneStart(buf[end]) {
			end--
		}
		buf = buf[:end]
	} else if n := len(buf); n > 0 && buf[n-1] == '\r' {
		buf = buf[:n-1]
	}
	return Line{Text: validUTF8(buf), Truncated: truncated}
}

// Each invalid byte becomes U+FFFD.
func validUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out := make([]byte, 0, len(b)+8)
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		out = utf8.AppendRune(out, r)
		b = b[size:]
	}
	return string(out)
}
