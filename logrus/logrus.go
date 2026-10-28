// Package logifylogrus sends logrus logs to dlogify through a hook:
//
//	logger.AddHook(logifylogrus.NewHook())
//
// Records go through the client created by logify.Init unless WithClient
// names another one.
package logifylogrus

import (
	"log/slog"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	logify "dlogify.com/sdk/go"
)

const fatalFlush = 2 * time.Second

var skip = []string{"github.com/sirupsen/logrus"}

type Option func(*hook)

// WithClient sends through c instead of the client created by logify.Init.
func WithClient(c *logify.Client) Option { return func(h *hook) { h.client = c } }

func NewHook(opts ...Option) logrus.Hook {
	h := &hook{}
	for _, o := range opts {
		o(h)
	}
	return h
}

type hook struct{ client *logify.Client }

func (h *hook) c() *logify.Client {
	if h.client != nil {
		return h.client
	}
	return logify.Default()
}

func (h *hook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *hook) Fire(e *logrus.Entry) error {
	c := h.c()
	l := level(e.Level)
	if !c.Enabled(l) {
		return nil
	}
	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var err error
	attrs := make([]slog.Attr, 0, len(keys))
	for _, k := range keys {
		if k == logrus.ErrorKey && err == nil {
			if e, ok := e.Data[k].(error); ok {
				err = e
				continue
			}
		}
		attrs = append(attrs, slog.Any(k, e.Data[k]))
	}
	c.Emit(logify.Entry{Scope: "logrus", Level: l, Time: e.Time, Message: e.Message, Attrs: attrs, Err: err, Skip: skip})
	if l == logify.LevelFatal {
		c.Flush(fatalFlush) // logrus exits or panics right after
	}
	return nil
}

func level(l logrus.Level) logify.Level {
	switch l {
	case logrus.PanicLevel, logrus.FatalLevel:
		return logify.LevelFatal
	case logrus.ErrorLevel:
		return logify.LevelError
	case logrus.WarnLevel:
		return logify.LevelWarn
	case logrus.InfoLevel:
		return logify.LevelInfo
	case logrus.DebugLevel:
		return logify.LevelDebug
	}
	return logify.LevelTrace
}
