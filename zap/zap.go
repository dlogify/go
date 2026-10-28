// Package logifyzap sends zap logs to dlogify. Add its core next to yours:
//
//	core := zapcore.NewTee(yourCore, logifyzap.NewCore())
//	logger := zap.New(core)
//
// Records go through the client created by logify.Init unless WithClient
// names another one.
package logifyzap

import (
	"log/slog"
	"sort"
	"time"

	"go.uber.org/zap/zapcore"

	logify "dlogify.com/sdk/go"
)

const fatalFlush = 2 * time.Second

var skip = []string{"go.uber.org/zap"}

type Option func(*core)

// WithClient sends through c instead of the client created by logify.Init.
func WithClient(c *logify.Client) Option { return func(k *core) { k.client = c } }

func NewCore(opts ...Option) zapcore.Core {
	k := &core{}
	for _, o := range opts {
		o(k)
	}
	return k
}

type core struct {
	client *logify.Client
	fields []zapcore.Field
}

func (k *core) c() *logify.Client {
	if k.client != nil {
		return k.client
	}
	return logify.Default()
}

func (k *core) Enabled(l zapcore.Level) bool { return k.c().Enabled(level(l)) }

func (k *core) With(fields []zapcore.Field) zapcore.Core {
	n := *k
	n.fields = append(append([]zapcore.Field(nil), k.fields...), fields...)
	return &n
}

func (k *core) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if k.Enabled(e.Level) {
		return ce.AddCore(e, k)
	}
	return ce
}

func (k *core) Write(e zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	var err error
	for _, f := range append(append([]zapcore.Field(nil), k.fields...), fields...) {
		if f.Type == zapcore.ErrorType && err == nil {
			if e, ok := f.Interface.(error); ok {
				err = e
				continue
			}
		}
		f.AddTo(enc)
	}
	keys := make([]string, 0, len(enc.Fields))
	for key := range enc.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var attrs []slog.Attr
	if e.LoggerName != "" {
		attrs = append(attrs, slog.String("logger.name", e.LoggerName))
	}
	for _, key := range keys {
		attrs = append(attrs, slog.Any(key, enc.Fields[key]))
	}
	c := k.c()
	l := level(e.Level)
	c.Emit(logify.Entry{Scope: "zap", Level: l, Time: e.Time, Message: e.Message, Attrs: attrs, Err: err, Skip: skip})
	if l == logify.LevelFatal {
		c.Flush(fatalFlush) // zap exits or panics right after
	}
	return nil
}

func (k *core) Sync() error {
	k.c().Flush(fatalFlush)
	return nil
}

func level(l zapcore.Level) logify.Level {
	switch {
	case l < zapcore.DebugLevel:
		return logify.LevelTrace
	case l == zapcore.DebugLevel:
		return logify.LevelDebug
	case l == zapcore.InfoLevel:
		return logify.LevelInfo
	case l == zapcore.WarnLevel:
		return logify.LevelWarn
	case l == zapcore.ErrorLevel, l == zapcore.DPanicLevel:
		return logify.LevelError
	}
	return logify.LevelFatal
}
