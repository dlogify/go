package logify

import (
	"context"
	"log/slog"
)

// Frames of these packages are removed from the top of a record's stack: the
// log package goes through slog after slog.SetDefault.
var slogSkip = []string{"log/slog", "log"}

// NewSlogHandler returns a handler that passes every record to next unchanged
// (when next is not nil) and sends it to dlogify through the client created by
// Init. Install it with slog.SetDefault(slog.New(logify.NewSlogHandler(h))).
func NewSlogHandler(next slog.Handler) slog.Handler {
	return &slogHandler{next: next}
}

// NewSlogHandler is NewSlogHandler for this client.
func (c *Client) NewSlogHandler(next slog.Handler) slog.Handler {
	return &slogHandler{client: c, next: next}
}

type slogHandler struct {
	client *Client // nil: Default() at each call
	next   slog.Handler
	attrs  []slog.Attr // from WithAttrs, already nested in their groups
	groups []string
}

func (h *slogHandler) c() *Client {
	if h.client != nil {
		return h.client
	}
	return Default()
}

func (h *slogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	if h.next != nil && h.next.Enabled(ctx, l) {
		return true
	}
	return h.c().Enabled(levelFromSlog(l))
}

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if h.next != nil && h.next.Enabled(ctx, r.Level) {
		err = h.next.Handle(ctx, r)
	}
	c := h.c()
	level := levelFromSlog(r.Level)
	if c.Enabled(level) {
		attrs := make([]slog.Attr, 0, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			attrs = append(attrs, a)
			return true
		})
		all := append(append([]slog.Attr(nil), h.attrs...), wrapGroups(h.groups, attrs)...)
		c.Emit(Entry{Scope: "slog", Level: level, Time: r.Time, Message: r.Message, Attrs: all, Skip: slogSkip})
	}
	return err
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	n := *h
	if h.next != nil {
		n.next = h.next.WithAttrs(attrs)
	}
	n.attrs = append(append([]slog.Attr(nil), h.attrs...), wrapGroups(h.groups, attrs)...)
	return &n
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	if h.next != nil {
		n.next = h.next.WithGroup(name)
	}
	n.groups = append(append([]string(nil), h.groups...), name)
	return &n
}

// wrapGroups nests attrs in the open groups; flattenAttrs turns them into
// dotted keys.
func wrapGroups(groups []string, attrs []slog.Attr) []slog.Attr {
	if len(groups) == 0 || len(attrs) == 0 {
		return attrs
	}
	for i := len(groups) - 1; i >= 0; i-- {
		attrs = []slog.Attr{{Key: groups[i], Value: slog.GroupValue(attrs...)}}
	}
	return attrs
}

// Custom levels fall to the nearest lower standard one.
func levelFromSlog(l slog.Level) Level {
	switch {
	case l < slog.LevelDebug:
		return LevelTrace
	case l < slog.LevelInfo:
		return LevelDebug
	case l < slog.LevelWarn:
		return LevelInfo
	case l < slog.LevelError:
		return LevelWarn
	case l < slog.LevelError+4:
		return LevelError
	}
	return LevelFatal
}
