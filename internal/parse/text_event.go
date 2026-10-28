package parse

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"dlogify.com/sdk/go/internal/record"
)

// docs/12 §5.3–5.6.
const traceback = "Traceback (most recent call last):"

var (
	chainLines = map[string]bool{
		"During handling of the above exception, another exception occurred:":  true,
		"The above exception was the direct cause of the following exception:": true,
	}
	prefixLevel     = regexp.MustCompile(`(?i)^[\[(]?(` + levelWordPattern + `)`)
	upperLevel      = regexp.MustCompile(`(` + levelWordPattern + `)`)
	leadingToken    = regexp.MustCompile(`^\s*(\S+)`)
	timestampToken  = regexp.MustCompile(`^[0-9\-:.,/+TZ]+$`)
	pyExceptionLine = regexp.MustCompile(`^([A-Za-z_][\w.]*)(?:: (.*))?$`)
	header          = regexp.MustCompile(`^(?:Uncaught )?(?:Exception in thread "[^"]*" )?((?:[A-Za-z_$][\w$.]*)?(?:Error|Exception))(?: \[[\w-]+\])?: (.*)$`)
	headerTypeOnly  = regexp.MustCompile(`^((?:[A-Za-z_$][\w$.]*)?(?:Error|Exception))$`)
	javaMore        = regexp.MustCompile(`^\.\.\. \d+ (more|common frames omitted)$`)
	nodeVersion     = regexp.MustCompile(`^Node\.js v\d+`)
	goPanic         = regexp.MustCompile(`^panic: (.*?)(?: \[recovered\])?$`)
	goFatal         = regexp.MustCompile(`^fatal error: (.*)$`)
	leadingSpace    = regexp.MustCompile(`^\s`)
	stackLines      = []*regexp.Regexp{regexp.MustCompile(`^\s+at `), regexp.MustCompile(`^\s+File "`), regexp.MustCompile(`\.go:\d+`)}
)

const upperWindow = 64

type Parsed struct {
	Severity int
	Body     string
	Time     int64
	Attrs    []record.Attr
}

type Exception struct {
	Type, Message, Stacktrace string
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

func isASCIIWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// DetectLevel applies §5.3 to an event's first line.
func DetectLevel(line string) (int, bool) {
	rest := line
	for i := 0; i < 2; i++ {
		m := leadingToken.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		token := rest[m[2]:m[3]]
		if !timestampToken.MatchString(token) || !strings.ContainsAny(token, "0123456789") {
			break
		}
		rest = rest[m[1]:]
	}
	trimmed := strings.TrimLeftFunc(rest, unicode.IsSpace)
	if m := prefixLevel.FindStringSubmatchIndex(trimmed); m != nil {
		end := m[1]
		ok := end == len(trimmed)
		if !ok {
			r, _ := utf8.DecodeRuneInString(trimmed[end:])
			ok = r == ']' || r == ')' || r == ':' || r == '|' || unicode.IsSpace(r)
		}
		if ok {
			return LevelFromWord(trimmed[m[2]:m[3]])
		}
	}
	for _, m := range upperLevel.FindAllStringIndex(line, -1) {
		start, end := m[0], m[1]
		if start > 0 && isASCIIWord(line[start-1]) || end < len(line) && isASCIIWord(line[end]) {
			continue
		}
		if utf8.RuneCountInString(line[:end]) <= upperWindow {
			return LevelFromWord(line[start:end])
		}
		return 0, false
	}
	return 0, false
}

func exceptionHeader(line string) (typ, msg string, ok bool) {
	if m := header.FindStringSubmatch(line); m != nil {
		return m[1], m[2], true
	}
	if m := headerTypeOnly.FindStringSubmatch(line); m != nil {
		return m[1], "", true
	}
	return "", "", false
}

// ExtractException applies §5.6.
func ExtractException(lines []string) (Exception, bool) {
	tb := -1
	for i, l := range lines {
		if l == traceback {
			tb = i
			break
		}
	}
	if tb >= 0 {
		for i := len(lines) - 1; i > tb; i-- {
			if m := pyExceptionLine.FindStringSubmatch(lines[i]); m != nil {
				return Exception{Type: m[1], Message: m[2], Stacktrace: strings.Join(lines[tb:], "\n")}, true
			}
		}
	}
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	if m := goPanic.FindStringSubmatch(first); m != nil {
		return Exception{Type: "panic", Message: m[1], Stacktrace: strings.Join(lines, "\n")}, true
	}
	if m := goFatal.FindStringSubmatch(first); m != nil {
		return Exception{Type: "fatal error", Message: m[1], Stacktrace: strings.Join(lines, "\n")}, true
	}
	for i, l := range lines {
		if typ, msg, ok := exceptionHeader(l); ok {
			return Exception{Type: typ, Message: msg, Stacktrace: strings.Join(lines[i:], "\n")}, true
		}
	}
	return Exception{}, false
}

// continuesEvent applies the six rules of §5.4.
func continuesEvent(event []string, line string) bool {
	first, last := "", ""
	if len(event) > 0 {
		first, last = event[0], event[len(event)-1]
	}
	// 1. Indented, non-blank.
	if !isBlank(line) && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
		return true
	}
	// 2. Java causes and omitted frames, Python traceback starts.
	if strings.HasPrefix(line, "Caused by: ") || javaMore.MatchString(line) || line == traceback {
		return true
	}
	// 3. Inside a Python traceback.
	if contains(event, traceback) {
		if chainLines[line] {
			return true
		}
		if pyExceptionLine.MatchString(line) {
			lastNonBlank := ""
			for i := len(event) - 1; i >= 0; i-- {
				if !isBlank(event[i]) {
					lastNonBlank = event[i]
					break
				}
			}
			if leadingSpace.MatchString(lastNonBlank) || lastNonBlank == traceback {
				return true
			}
		}
	}
	// 4. Go panics take every following text line.
	if strings.HasPrefix(first, "panic: ") || strings.HasPrefix(first, "fatal error: ") {
		return true
	}
	// 5. After a blank line: an exception header or Node's version line.
	if isBlank(last) {
		if _, _, ok := exceptionHeader(line); ok || nodeVersion.MatchString(line) {
			return true
		}
	}
	// 6. A blank line inside a multi-line event or after a header (kept tentatively).
	if isBlank(line) {
		if len(event) > 1 {
			return true
		}
		if _, _, ok := exceptionHeader(first); ok {
			return true
		}
	}
	return false
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}

// buildTextRecord applies §5.5; false when the event is only blank lines.
func buildTextRecord(input []string, truncated bool) (Parsed, bool) {
	lines := input
	for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return Parsed{}, false
	}
	detected, hasLevel := DetectLevel(lines[0])
	exc, hasExc := ExtractException(lines)
	hasStack := hasExc
	for _, l := range lines {
		if hasStack {
			break
		}
		for _, re := range stackLines {
			if re.MatchString(l) {
				hasStack = true
				break
			}
		}
	}
	severity := detected
	if !hasLevel {
		severity = record.SeverityUnknown
		if hasStack {
			severity = record.SeverityError
		}
	}
	body := lines[0]
	if hasExc && !hasLevel {
		body = record.ExceptionTitle(exc.Type, exc.Message)
	}
	p := Parsed{Severity: severity, Body: strings.TrimRightFunc(body, unicode.IsSpace)}
	if hasExc {
		p.Attrs = append(p.Attrs,
			record.Attr{Key: "exception.type", Value: record.String(exc.Type)},
			record.Attr{Key: "exception.message", Value: record.String(exc.Message)},
			record.Attr{Key: "exception.stacktrace", Value: record.String(exc.Stacktrace)},
		)
	}
	if truncated {
		p.Attrs = append(p.Attrs, record.Attr{Key: "logify.truncated", Value: record.Bool(true)})
	}
	return p, true
}
