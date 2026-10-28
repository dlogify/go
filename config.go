package logify

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"dlogify.com/sdk/go/internal/diag"
	"dlogify.com/sdk/go/internal/record"
)

// docs/12 §1: option > environment variable > default. Never panics for bad
// values: it falls back to the default and reports a warning.
const (
	defaultEndpoint     = "https://api.dlogify.com"
	defaultMaxQueueSize = 2000
)

var readBuildInfo = debug.ReadBuildInfo

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

type config struct {
	apiKey         string
	logsURL        string
	service        string
	environment    string
	release        string
	minLevel       Level
	attributes     []record.Attr
	maxQueueSize   int
	enabled        bool
	debug          bool
	redactDefaults bool
	redactKeys     []string
}

var levelNames = map[string]Level{"trace": LevelTrace, "debug": LevelDebug, "info": LevelInfo, "warn": LevelWarn, "error": LevelError, "fatal": LevelFatal}

func resolveConfig(opts Options, getenv func(string) string) (config, []string) {
	var warnings []string
	fromEnv := func(name string) string { return strings.TrimSpace(getenv(name)) }
	read := func(option, name string) string {
		if v := strings.TrimSpace(option); v != "" {
			return v
		}
		return fromEnv(name)
	}

	cfg := config{
		apiKey:         read(opts.APIKey, "LOGIFY_API_KEY"),
		debug:          opts.Debug || diag.IsFlag(getenv("LOGIFY_DEBUG")),
		redactDefaults: opts.RedactDefaults || diag.IsFlag(getenv("LOGIFY_REDACT_DEFAULTS")),
		environment:    read(opts.Environment, "LOGIFY_ENVIRONMENT"),
		release:        read(opts.Release, "LOGIFY_RELEASE"),
		minLevel:       LevelInfo,
		maxQueueSize:   defaultMaxQueueSize,
	}
	disabled := opts.Disabled || diag.IsFlag(getenv("LOGIFY_DISABLED"))
	cfg.enabled = !disabled
	if cfg.enabled && cfg.apiKey == "" {
		cfg.enabled = false
		warnings = append(warnings, "LOGIFY_API_KEY is not set; Logify is disabled")
	}

	endpoint := defaultEndpoint
	if raw := read(opts.Endpoint, "LOGIFY_ENDPOINT"); raw != "" {
		if parsed, ok := parseEndpoint(raw); ok {
			endpoint = parsed
		} else {
			warnings = append(warnings, fmt.Sprintf("invalid endpoint %q; using %s", raw, defaultEndpoint))
		}
	}
	cfg.logsURL = endpoint + "/v1/logs"

	if cfg.environment == "" {
		cfg.environment = "default"
	}
	if cfg.service = read(opts.Service, "LOGIFY_SERVICE"); cfg.service == "" {
		cfg.service = defaultService(fromEnv)
	}

	if opts.MinLevel != 0 {
		if opts.MinLevel.valid() {
			cfg.minLevel = opts.MinLevel
		} else {
			warnings = append(warnings, fmt.Sprintf("unknown level %s; using info", opts.MinLevel))
		}
	} else if raw := strings.ToLower(fromEnv("LOGIFY_MIN_LEVEL")); raw != "" {
		if l, ok := levelNames[raw]; ok {
			cfg.minLevel = l
		} else {
			warnings = append(warnings, fmt.Sprintf("unknown level %q; using info", raw))
		}
	}

	if opts.MaxQueueSize > 0 {
		cfg.maxQueueSize = opts.MaxQueueSize
	} else if opts.MaxQueueSize < 0 {
		warnings = append(warnings, fmt.Sprintf("invalid MaxQueueSize %d; using %d", opts.MaxQueueSize, defaultMaxQueueSize))
	}

	keys := make([]string, 0, len(opts.Attributes))
	for k := range opts.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := record.Normalize(opts.Attributes[k]); ok {
			cfg.attributes = append(cfg.attributes, record.Attr{Key: k, Value: v})
		}
	}

	if opts.RedactKeys != nil {
		cfg.redactKeys = opts.RedactKeys
	} else {
		for _, k := range strings.Split(fromEnv("LOGIFY_REDACT_KEYS"), ",") {
			if k = strings.TrimSpace(k); k != "" {
				cfg.redactKeys = append(cfg.redactKeys, k)
			}
		}
	}
	return cfg, warnings
}

func parseEndpoint(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" ||
		u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	return strings.TrimRight(raw, "/"), true
}

func defaultService(fromEnv func(string) string) string {
	if v := fromEnv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	if info, ok := readBuildInfo(); ok && info != nil {
		p := info.Main.Path
		if p != "" && p != "command-line-arguments" {
			base := path.Base(p)
			if majorVersion.MatchString(base) && strings.Contains(p, "/") {
				base = path.Base(path.Dir(p))
			}
			return base
		}
	}
	if exe, err := os.Executable(); err == nil {
		if name := strings.TrimSuffix(filepath.Base(exe), ".exe"); name != "" {
			return name
		}
	}
	return "unknown_service"
}
