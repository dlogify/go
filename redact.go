package logify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dlogify.com/sdk/go/internal/goid"
	"dlogify.com/sdk/go/internal/record"
	"dlogify.com/sdk/go/internal/redact"
)

// docs/12 §3.1: key rules, pattern rules, built-in rules, then BeforeSend.
// The size cuts run after this, in record.Finalize.
const redactedPlaceholder = "<redacted>"

type redactor struct {
	defaults bool
	keys     []func(string) bool
	patterns []RedactPattern
	before   func(*Event) *Event
}

// newRedactor returns nil when nothing is configured: the client then skips
// redaction. Invalid entries are skipped with a warning the client always
// prints (a privacy rule that silently does not apply is worse than a noisy
// start).
func newRedactor(cfg config, opts Options) (*redactor, []string) {
	var warnings []string
	r := &redactor{defaults: cfg.redactDefaults, before: opts.BeforeSend}
	for i, k := range cfg.redactKeys {
		k = strings.TrimSpace(k)
		if k == "" {
			warnings = append(warnings, fmt.Sprintf("RedactKeys[%d] is empty; ignored", i))
			continue
		}
		lower := redact.ASCIILower(k)
		r.keys = append(r.keys, func(key string) bool { return redact.ASCIILower(key) == lower })
	}
	for i, p := range opts.RedactKeyPatterns {
		if p == nil {
			warnings = append(warnings, fmt.Sprintf("RedactKeyPatterns[%d] is nil; ignored", i))
			continue
		}
		r.keys = append(r.keys, p.MatchString)
	}
	for i, p := range opts.RedactPatterns {
		if p.Pattern == nil {
			warnings = append(warnings, fmt.Sprintf("RedactPatterns[%d] has no Pattern; ignored", i))
			continue
		}
		if p.Replacement == "" {
			p.Replacement = redactedPlaceholder
		}
		r.patterns = append(r.patterns, p)
	}
	if !r.defaults && len(r.keys) == 0 && len(r.patterns) == 0 && r.before == nil {
		return nil, warnings
	}
	return r, warnings
}

// apply redacts rec in place; false drops the record.
func (r *redactor) apply(c *Client, rec *record.Record) bool {
	replaced := make([]bool, len(rec.Attrs))
	for i, a := range rec.Attrs {
		for _, matches := range r.keys {
			if matches(a.Key) {
				rec.Attrs[i].Value = record.String(redactedPlaceholder)
				replaced[i] = true
				break
			}
		}
	}
	rewrite := func(fn func(string) string) {
		for i := range rec.Attrs {
			if !replaced[i] && rec.Attrs[i].Value.Kind == record.KindString {
				rec.Attrs[i].Value.Str = fn(rec.Attrs[i].Value.Str)
			}
		}
	}
	for _, p := range r.patterns {
		replace := func(s string) string { return p.Pattern.ReplaceAllLiteralString(s, p.Replacement) }
		rec.Body = replace(rec.Body)
		rewrite(replace)
	}
	if r.defaults {
		rec.Body = redact.Text(rec.Body)
		for i := range rec.Attrs {
			if !replaced[i] && redact.IsSensitiveKey(rec.Attrs[i].Key) {
				rec.Attrs[i].Value = record.String(redact.Secret)
				replaced[i] = true
			}
		}
		rewrite(redact.Text)
	}
	if r.before != nil {
		return c.runHook(r.before, rec)
	}
	return true
}

// runHook calls BeforeSend with this goroutine marked, so a record logged
// from inside the hook is dropped (docs/12 §3.1) instead of recursing.
func (c *Client) runHook(hook func(*Event) *Event, rec *record.Record) bool {
	id := goid.Get()
	c.inHook.Store(id, struct{}{})
	defer c.inHook.Delete(id)

	attrs := make(map[string]any, len(rec.Attrs))
	for _, a := range rec.Attrs {
		attrs[a.Key] = a.Value.Native()
	}
	event := &Event{Level: Level(rec.Severity), Message: rec.Body, Attributes: attrs, Scope: rec.Scope, Time: time.Unix(0, rec.Time)}
	var out *Event
	failure := ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprintf("BeforeSend panicked: %v", r)
			}
		}()
		out = hook(event)
	}()
	if failure != "" {
		if n := c.hookFailures.Add(1); n == 1 {
			c.diag.Warn("%s; the record was dropped. Later failures are counted with LOGIFY_DEBUG", failure)
		} else {
			c.diag.Debug("%s; dropped a record (%d failures in total)", failure, n)
		}
		return false
	}
	if out == nil {
		c.diag.Debug("BeforeSend dropped a record (%d in total)", c.hookDrops.Add(1))
		return false
	}
	rec.Body = out.Message
	rec.Attrs = attrsFromMap(out.Attributes, rec.Attrs)
	return true
}

// Keys the record had keep their order; new keys follow, sorted.
func attrsFromMap(m map[string]any, before []record.Attr) []record.Attr {
	var out []record.Attr
	seen := map[string]bool{}
	for _, a := range before {
		seen[a.Key] = true
		if v, ok := m[a.Key]; ok {
			if rv, ok := record.Normalize(v); ok {
				out = append(out, record.Attr{Key: a.Key, Value: rv})
			}
		}
	}
	var added []string
	for k := range m {
		if !seen[k] {
			added = append(added, k)
		}
	}
	sort.Strings(added)
	for _, k := range added {
		if rv, ok := record.Normalize(m[k]); ok {
			out = append(out, record.Attr{Key: k, Value: rv})
		}
	}
	return out
}
