// Package logifyzerolog sends zerolog logs to dlogify. Add its writer next to
// yours:
//
//	logger := zerolog.New(zerolog.MultiLevelWriter(os.Stdout, logifyzerolog.Writer()))
//
// zerolog hands writers the error as text, so records carry
// exception.message without exception.type. Records go through the client
// created by logify.Init unless WithClient names another one.
package logifyzerolog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"

	logify "dlogify.com/sdk/go"
)

const fatalFlush = 2 * time.Second

var skip = []string{"github.com/rs/zerolog"}

type Option func(*writer)

// WithClient sends through c instead of the client created by logify.Init.
func WithClient(c *logify.Client) Option { return func(w *writer) { w.client = c } }

func Writer(opts ...Option) zerolog.LevelWriter {
	w := &writer{}
	for _, o := range opts {
		o(w)
	}
	return w
}

type writer struct{ client *logify.Client }

func (w *writer) c() *logify.Client {
	if w.client != nil {
		return w.client
	}
	return logify.Default()
}

func (w *writer) Write(p []byte) (int, error) { return w.WriteLevel(zerolog.NoLevel, p) }

func (w *writer) WriteLevel(l zerolog.Level, p []byte) (int, error) {
	n := len(p)
	lvl, ok := level(l)
	c := w.c()
	if !ok || !c.Enabled(lvl) {
		return n, nil
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var fields map[string]any
	if dec.Decode(&fields) != nil {
		c.Emit(logify.Entry{Scope: "zerolog", Level: lvl, Message: strings.TrimSpace(string(p)), Skip: skip})
		return n, nil
	}
	msg, _ := fields[zerolog.MessageFieldName].(string)
	errText, _ := fields[zerolog.ErrorFieldName].(string)
	when := parseTime(fields[zerolog.TimestampFieldName])
	delete(fields, zerolog.MessageFieldName)
	delete(fields, zerolog.LevelFieldName)
	delete(fields, zerolog.TimestampFieldName)
	if errText != "" {
		delete(fields, zerolog.ErrorFieldName)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make([]slog.Attr, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, slog.Any(k, fields[k]))
	}
	c.Emit(logify.Entry{Scope: "zerolog", Level: lvl, Time: when, Message: msg, Attrs: attrs, ErrText: errText, Skip: skip})
	if lvl == logify.LevelFatal {
		c.Flush(fatalFlush) // zerolog exits or panics right after
	}
	return n, nil
}

func level(l zerolog.Level) (logify.Level, bool) {
	switch l {
	case zerolog.Disabled:
		return 0, false
	case zerolog.TraceLevel:
		return logify.LevelTrace, true
	case zerolog.DebugLevel:
		return logify.LevelDebug, true
	case zerolog.InfoLevel, zerolog.NoLevel:
		return logify.LevelInfo, true
	case zerolog.WarnLevel:
		return logify.LevelWarn, true
	case zerolog.ErrorLevel:
		return logify.LevelError, true
	case zerolog.FatalLevel, zerolog.PanicLevel:
		return logify.LevelFatal, true
	}
	if l < zerolog.TraceLevel {
		return logify.LevelTrace, true
	}
	return logify.LevelInfo, true
}

// parseTime reads the timestamp in zerolog's configured format; the zero
// time means "now".
func parseTime(v any) time.Time {
	switch x := v.(type) {
	case string:
		if t, err := time.Parse(zerolog.TimeFieldFormat, x); err == nil {
			return t
		}
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return time.Time{}
		}
		switch zerolog.TimeFieldFormat {
		case zerolog.TimeFormatUnix:
			return time.Unix(n, 0)
		case zerolog.TimeFormatUnixMs:
			return time.UnixMilli(n)
		case zerolog.TimeFormatUnixMicro:
			return time.UnixMicro(n)
		case zerolog.TimeFormatUnixNano:
			return time.Unix(0, n)
		}
	}
	return time.Time{}
}
