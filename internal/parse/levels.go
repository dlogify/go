// Package parse implements docs/12 §5: log lines from a process's stdout and
// stderr become records (JSON lines, multiline text events, levels and
// exceptions). Used by logify run.
package parse

import (
	"math"
	"strings"

	"dlogify.com/sdk/go/internal/record"
)

// Longer words first, so an alternation prefers WARNING over WARN.
const levelWordPattern = "WARNING|WARN|ERROR|ERR|TRACE|DEBUG|INFO|NOTICE|FATAL|CRITICAL|PANIC"

var levelWords = map[string]int{
	"TRACE": record.SeverityTrace, "DEBUG": record.SeverityDebug,
	"INFO": record.SeverityInfo, "NOTICE": record.SeverityInfo,
	"WARN": record.SeverityWarn, "WARNING": record.SeverityWarn,
	"ERROR": record.SeverityError, "ERR": record.SeverityError,
	"FATAL": record.SeverityFatal, "CRITICAL": record.SeverityFatal, "PANIC": record.SeverityFatal,
}

func LevelFromWord(word string) (int, bool) {
	n, ok := levelWords[strings.ToUpper(strings.TrimSpace(word))]
	return n, ok
}

// LevelFromNumber reads the pino/bunyan scale; custom numbers map to the
// nearest lower standard level.
func LevelFromNumber(n float64) (int, bool) {
	switch {
	case math.IsNaN(n) || math.IsInf(n, 0) || n < 10:
		return 0, false
	case n >= 60:
		return record.SeverityFatal, true
	case n >= 50:
		return record.SeverityError, true
	case n >= 40:
		return record.SeverityWarn, true
	case n >= 30:
		return record.SeverityInfo, true
	case n >= 20:
		return record.SeverityDebug, true
	}
	return record.SeverityTrace, true
}
