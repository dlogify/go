package logify

import (
	"fmt"
	"runtime"
	"strings"

	"dlogify.com/sdk/go/internal/goid"
	"dlogify.com/sdk/go/internal/record"
)

// docs/12 §3 Errors, Go form (contract change "Go SDK").
const (
	maxCauses    = 5
	maxFrames    = 64
	modulePrefix = "dlogify.com/sdk/go"
)

func safeError(err error) (s string) {
	defer func() {
		if recover() != nil {
			s = fmt.Sprintf("%T", err)
		}
	}()
	return err.Error()
}

func exceptionTitle(err error, errText string) string {
	if err == nil {
		return errText
	}
	return record.ExceptionTitle(fmt.Sprintf("%T", err), safeError(err))
}

// exceptionAttrs builds exception.type (only for a real error),
// exception.message and exception.stacktrace (the stack, then one
// "Caused by" line per wrapped error).
func exceptionAttrs(err error, errText, stack string) []record.Attr {
	var attrs []record.Attr
	msg := errText
	if err != nil {
		attrs = append(attrs, record.Attr{Key: "exception.type", Value: record.String(fmt.Sprintf("%T", err))})
		msg = safeError(err)
	}
	return append(attrs,
		record.Attr{Key: "exception.message", Value: record.String(msg)},
		record.Attr{Key: "exception.stacktrace", Value: record.String(stack + causeLines(err))},
	)
}

// Depth first over Unwrap() error and Unwrap() []error, at most maxCauses.
func causeLines(err error) (out string) {
	if err == nil {
		return ""
	}
	var b strings.Builder
	defer func() {
		if recover() != nil {
			out = b.String()
		}
	}()
	n := 0
	var walk func(e error)
	walk = func(e error) {
		var children []error
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			if c := u.Unwrap(); c != nil {
				children = []error{c}
			}
		case interface{ Unwrap() []error }:
			children = u.Unwrap()
		}
		for _, c := range children {
			if c == nil || n >= maxCauses {
				continue
			}
			n++
			b.WriteString("\nCaused by: " + record.ExceptionTitle(fmt.Sprintf("%T", c), safeError(c)))
			walk(c)
		}
	}
	walk(err)
	return b.String()
}

func hasPathPrefix(function, prefix string) bool {
	return strings.HasPrefix(function, prefix+".") || strings.HasPrefix(function, prefix+"/")
}

func callers(skip int) []runtime.Frame {
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(skip+1, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var out []runtime.Frame
	for {
		f, more := frames.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	return out
}

// callerStack is the stack of the logging call, without the innermost frames
// of this module (adapters included) and of the logging libraries in skip.
func callerStack(skip []string) string {
	frames := callers(2)
	i := 0
	for i < len(frames) && isLibraryFrame(frames[i].Function, skip) {
		i++
	}
	return formatStack(frames[i:])
}

func isLibraryFrame(function string, skip []string) bool {
	if strings.Contains(function, "_test.") {
		return false // this module's own tests (dlogify.com/sdk/go/zap_test, …) are application code
	}
	if hasPathPrefix(function, modulePrefix) {
		return true
	}
	for _, p := range skip {
		if hasPathPrefix(function, p) {
			return true
		}
	}
	return false
}

// panicStack, called from a deferred Recover, keeps the frames below the
// panic: everything up to runtime.gopanic goes, then the runtime's own panic
// helpers (runtime.panicIndex, runtime.sigpanic, …).
func panicStack() string {
	frames := callers(2)
	for i, f := range frames {
		if f.Function == "runtime.gopanic" {
			frames = frames[i+1:]
			break
		}
	}
	for len(frames) > 0 && (strings.HasPrefix(frames[0].Function, "runtime.") || hasPathPrefix(frames[0].Function, modulePrefix)) {
		frames = frames[1:]
	}
	return formatStack(frames)
}

// The format of a Go goroutine dump, which the engine parses
// (services/engine/src/pipeline/frames.rs).
func formatStack(frames []runtime.Frame) string {
	var b strings.Builder
	fmt.Fprintf(&b, "goroutine %d [running]:", goid.Get())
	for _, f := range frames {
		function := f.Function
		if function == "" {
			function = "?"
		}
		fmt.Fprintf(&b, "\n%s(...)\n\t%s:%d", function, f.File, f.Line)
		if f.Entry != 0 && f.PC >= f.Entry {
			fmt.Fprintf(&b, " +0x%x", f.PC-f.Entry)
		}
	}
	return b.String()
}
