// Package config loads KubePilot API configuration from the environment.
//
// Twelve-factor style: every setting has a sane default so `go run ./cmd/api`
// works with zero config, and every setting is overridable for containers.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved runtime configuration for the API server.
type Config struct {
	// Addr is the host:port the HTTP server binds to.
	Addr string
	// Kubeconfig is an explicit path to a kubeconfig file. Empty means: try the
	// in-cluster config first, then fall back to the standard kubeconfig
	// resolution (KUBECONFIG / ~/.kube/config).
	Kubeconfig string
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// LogFormat is json (production) or console (human-friendly local dev).
	LogFormat string
	// ShutdownTimeout bounds graceful shutdown of in-flight requests.
	ShutdownTimeout time.Duration

	// Prometheus configures the optional metrics backend used by capacity
	// planning. When PrometheusURL is empty, capacity analysis falls back to
	// API-server-only data (density and commitment) with no utilization trends.
	Prometheus PrometheusConfig

	// AI configures the optional Claude-backed explanation layer. When APIKey is
	// empty the explain endpoint returns 503; all deterministic analysis is
	// unaffected.
	AI AIConfig
}

// AIConfig holds the Claude API settings for the explanation layer.
type AIConfig struct {
	APIKey    string
	Model     string
	MaxTokens int64
}

// PrometheusConfig holds the capacity-planning Prometheus settings. The PromQL
// queries return a per-node utilization fraction labeled by `node`; defaults
// target node-exporter as deployed by kube-prometheus-stack. Override them to
// match a different metrics stack.
type PrometheusConfig struct {
	URL           string
	LookbackHours float64
	StepMinutes   int
	CPUQuery      string
	MemQuery      string
}

const (
	defaultCPUQuery = `1 - avg by (node) (rate(node_cpu_seconds_total{mode="idle"}[5m]))`
	defaultMemQuery = `1 - avg by (node) (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
)

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Addr:            env("KUBEPILOT_ADDR", ":8080"),
		Kubeconfig:      env("KUBEPILOT_KUBECONFIG", os.Getenv("KUBECONFIG")),
		LogLevel:        strings.ToLower(env("KUBEPILOT_LOG_LEVEL", "info")),
		LogFormat:       strings.ToLower(env("KUBEPILOT_LOG_FORMAT", "json")),
		ShutdownTimeout: envDuration("KUBEPILOT_SHUTDOWN_TIMEOUT", 15*time.Second),
		Prometheus: PrometheusConfig{
			URL:           os.Getenv("KUBEPILOT_PROMETHEUS_URL"),
			LookbackHours: envFloat("KUBEPILOT_PROMETHEUS_LOOKBACK_HOURS", 6),
			StepMinutes:   envInt("KUBEPILOT_PROMETHEUS_STEP_MINUTES", 30),
			CPUQuery:      env("KUBEPILOT_PROMETHEUS_CPU_QUERY", defaultCPUQuery),
			MemQuery:      env("KUBEPILOT_PROMETHEUS_MEM_QUERY", defaultMemQuery),
		},
		AI: AIConfig{
			APIKey:    os.Getenv("KUBEPILOT_ANTHROPIC_API_KEY"),
			Model:     env("KUBEPILOT_AI_MODEL", "claude-opus-4-8"),
			MaxTokens: int64(envInt("KUBEPILOT_AI_MAX_TOKENS", 1024)),
		},
	}
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
