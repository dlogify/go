package logify

import (
	"runtime/debug"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestDefaults(t *testing.T) {
	defer stubBuildInfo("github.com/acme/checkout/v2")()
	cfg, warnings := resolveConfig(Options{}, env(map[string]string{"LOGIFY_API_KEY": "k"}))
	if len(warnings) != 0 {
		t.Fatalf("warnings %v", warnings)
	}
	if !cfg.enabled || cfg.logsURL != "https://api.dlogify.com/v1/logs" || cfg.service != "checkout" ||
		cfg.environment != "default" || cfg.release != "" || cfg.minLevel != LevelInfo || cfg.maxQueueSize != 2000 || cfg.debug {
		t.Fatalf("got %+v", cfg)
	}
}

func TestOptionsBeatEnvironment(t *testing.T) {
	cfg, _ := resolveConfig(Options{APIKey: "code", Service: "svc", Endpoint: "http://localhost:8080/", MinLevel: LevelWarn, MaxQueueSize: 10},
		env(map[string]string{"LOGIFY_API_KEY": "env", "LOGIFY_SERVICE": "envsvc", "LOGIFY_ENDPOINT": "https://x.example", "LOGIFY_MIN_LEVEL": "debug"}))
	if cfg.apiKey != "code" || cfg.service != "svc" || cfg.logsURL != "http://localhost:8080/v1/logs" || cfg.minLevel != LevelWarn || cfg.maxQueueSize != 10 {
		t.Fatalf("got %+v", cfg)
	}
}

func TestEnvironmentValues(t *testing.T) {
	cfg, _ := resolveConfig(Options{}, env(map[string]string{
		"LOGIFY_API_KEY": "k", "LOGIFY_ENVIRONMENT": "prod", "LOGIFY_RELEASE": "1.2.3", "LOGIFY_MIN_LEVEL": "ERROR",
		"OTEL_SERVICE_NAME": "otel", "LOGIFY_DEBUG": "true", "LOGIFY_REDACT_DEFAULTS": "1", "LOGIFY_REDACT_KEYS": " ssn, ,customer_ref",
	}))
	if cfg.environment != "prod" || cfg.release != "1.2.3" || cfg.minLevel != LevelError || cfg.service != "otel" || !cfg.debug || !cfg.redactDefaults {
		t.Fatalf("got %+v", cfg)
	}
	if strings.Join(cfg.redactKeys, ",") != "ssn,customer_ref" {
		t.Fatalf("keys %q", cfg.redactKeys)
	}
}

func TestBooleansInCodeOnlyTurnOn(t *testing.T) {
	cfg, _ := resolveConfig(Options{APIKey: "k", Disabled: false}, env(map[string]string{"LOGIFY_DISABLED": "1"}))
	if cfg.enabled {
		t.Fatal("LOGIFY_DISABLED must win over a false option")
	}
	cfg, _ = resolveConfig(Options{APIKey: "k", Disabled: true}, env(nil))
	if cfg.enabled {
		t.Fatal("Disabled: true must disable")
	}
}

func TestInvalidValuesFallBackWithWarnings(t *testing.T) {
	cfg, warnings := resolveConfig(Options{APIKey: "k", Endpoint: "ftp://x", MinLevel: Level(3), MaxQueueSize: -1}, env(map[string]string{"LOGIFY_MIN_LEVEL": "loud"}))
	if cfg.logsURL != "https://api.dlogify.com/v1/logs" || cfg.minLevel != LevelInfo || cfg.maxQueueSize != 2000 || len(warnings) != 3 {
		t.Fatalf("got %+v %v", cfg, warnings)
	}
	cfg, warnings = resolveConfig(Options{}, env(nil))
	if cfg.enabled || len(warnings) != 1 || !strings.Contains(warnings[0], "LOGIFY_API_KEY") {
		t.Fatalf("missing key: %+v %v", cfg, warnings)
	}
}

func TestServiceFallsBackToExecutable(t *testing.T) {
	defer stubBuildInfo("command-line-arguments")()
	cfg, _ := resolveConfig(Options{APIKey: "k"}, env(nil))
	if cfg.service == "" || cfg.service == "command-line-arguments" {
		t.Fatalf("service %q", cfg.service)
	}
}

func TestResourceAttributesAreSorted(t *testing.T) {
	cfg, _ := resolveConfig(Options{APIKey: "k", Attributes: map[string]any{"b": 1, "a": "x", "skip": nil}}, env(nil))
	if len(cfg.attributes) != 2 || cfg.attributes[0].Key != "a" || cfg.attributes[1].Key != "b" {
		t.Fatalf("got %+v", cfg.attributes)
	}
}

func TestLevelString(t *testing.T) {
	if LevelWarn.String() != "warn" || Level(0).String() != "unset" || Level(4).String() != "Level(4)" {
		t.Fatal("level names")
	}
}

func stubBuildInfo(path string) func() {
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: path}}, true
	}
	return func() { readBuildInfo = prev }
}
