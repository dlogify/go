package logify

import (
	"regexp"
	"strconv"
	"time"
)

// Level is a record's level. Its values are the OTLP severity numbers; the
// zero value means "not set".
type Level int

const (
	LevelTrace Level = 1
	LevelDebug Level = 5
	LevelInfo  Level = 9
	LevelWarn  Level = 13
	LevelError Level = 17
	LevelFatal Level = 21
)

func (l Level) String() string {
	switch l {
	case 0:
		return "unset"
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	case LevelFatal:
		return "fatal"
	}
	return "Level(" + strconv.Itoa(int(l)) + ")"
}

func (l Level) valid() bool {
	switch l {
	case LevelTrace, LevelDebug, LevelInfo, LevelWarn, LevelError, LevelFatal:
		return true
	}
	return false
}

// RedactPattern replaces every match of Pattern in the body and in string
// attributes with Replacement (inserted literally; "<redacted>" when empty).
type RedactPattern struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// Event is what BeforeSend sees and returns. Changes to Level, Scope and Time
// are ignored.
type Event struct {
	Level      Level
	Message    string
	Attributes map[string]any
	Scope      string
	Time       time.Time
}

// Options configure a client. Each field falls back to its LOGIFY_*
// environment variable, then to its default (docs.dlogify.com/send-logs/go).
// A boolean set to true turns the setting on; false leaves the environment
// variable in charge.
type Options struct {
	// APIKey (LOGIFY_API_KEY) is required; without it the client does nothing.
	APIKey string
	// Endpoint (LOGIFY_ENDPOINT), default https://api.dlogify.com.
	Endpoint string
	// Service (LOGIFY_SERVICE), default OTEL_SERVICE_NAME, then the main
	// module's last path element, then the executable name.
	Service string
	// Environment (LOGIFY_ENVIRONMENT), default "default".
	Environment string
	// Release (LOGIFY_RELEASE) becomes service.version.
	Release string
	// MinLevel (LOGIFY_MIN_LEVEL), default LevelInfo.
	MinLevel Level
	// Attributes are added to every record's resource.
	Attributes map[string]any
	// MaxQueueSize is the number of records kept in memory, default 2000.
	MaxQueueSize int
	// Disabled (LOGIFY_DISABLED) turns the client into a no-op.
	Disabled bool
	// Debug (LOGIFY_DEBUG) prints the client's diagnostics to stderr.
	Debug bool
	// RedactDefaults (LOGIFY_REDACT_DEFAULTS) applies the engine's PII rules
	// before records leave the process.
	RedactDefaults bool
	// RedactKeys (LOGIFY_REDACT_KEYS, comma-separated) are attribute keys whose
	// values become "<redacted>", compared ignoring ASCII case.
	RedactKeys []string
	// RedactKeyPatterns redact the values of attributes whose key matches.
	RedactKeyPatterns []*regexp.Regexp
	// RedactPatterns run in order on the body and string attribute values.
	RedactPatterns []RedactPattern
	// BeforeSend sees every record last and returns it (changed or not), or
	// nil to drop it. It must not block. A panic drops the record.
	BeforeSend func(*Event) *Event
}
