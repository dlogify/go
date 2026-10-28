package parse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitterJoinsChunksAndRemovesCR(t *testing.T) {
	var s LineSplitter
	got := s.Push([]byte("hel"))
	got = append(got, s.Push([]byte("lo\r\nwor"))...)
	got = append(got, s.End()...)
	if len(got) != 2 || got[0].Text != "hello" || got[1].Text != "wor" || got[0].Truncated {
		t.Fatalf("got %#v", got)
	}
	if extra := s.End(); len(extra) != 0 {
		t.Fatalf("End twice: %#v", extra)
	}
}

func TestLongLineIsCut(t *testing.T) {
	var s LineSplitter
	line := strings.Repeat("é", MaxLineBytes) // 2 bytes each
	lines := s.Push([]byte(line + "\nnext\n"))
	if len(lines) != 2 || !lines[0].Truncated || len(lines[0].Text) > MaxLineBytes || !utf8.ValidString(lines[0].Text) {
		t.Fatalf("got %d lines, truncated %v, %d bytes", len(lines), lines[0].Truncated, len(lines[0].Text))
	}
	if lines[1].Text != "next" || lines[1].Truncated {
		t.Fatalf("next line: %#v", lines[1])
	}
}

func TestInvalidUTF8IsReplaced(t *testing.T) {
	var s LineSplitter
	lines := s.Push([]byte("a\xffb\n"))
	if lines[0].Text != "a\uFFFDb" {
		t.Fatalf("got %q", lines[0].Text)
	}
}

func TestStripANSI(t *testing.T) {
	if got := StripANSI("\x1b[31mERROR\x1b[0m boom\x1b[?25l"); got != "ERROR boom" {
		t.Fatalf("got %q", got)
	}
}

func TestLevelWords(t *testing.T) {
	for word, want := range map[string]int{"warning": 13, " Err ": 17, "PANIC": 21, "notice": 9, "trace": 1} {
		if got, ok := LevelFromWord(word); !ok || got != want {
			t.Fatalf("%q: %d %v", word, got, ok)
		}
	}
	if _, ok := LevelFromWord("verbose"); ok {
		t.Fatal("verbose is not a level word")
	}
	for n, want := range map[float64]int{10: 1, 25: 5, 30: 9, 45: 13, 50: 17, 70: 21} {
		if got, ok := LevelFromNumber(n); !ok || got != want {
			t.Fatalf("%v: %d", n, got)
		}
	}
	if _, ok := LevelFromNumber(5); ok {
		t.Fatal("5 is below the scale")
	}
}
