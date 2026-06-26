// Package config loads KubePilot API configuration from the environment.
//
// Twelve-factor style: every setting has a sane default so `go run ./cmd/api`
// works with zero config, and every setting is overridable for containers.
package config

import (
	"os"
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
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Addr:            env("KUBEPILOT_ADDR", ":8080"),
		Kubeconfig:      env("KUBEPILOT_KUBECONFIG", os.Getenv("KUBECONFIG")),
		LogLevel:        strings.ToLower(env("KUBEPILOT_LOG_LEVEL", "info")),
		LogFormat:       strings.ToLower(env("KUBEPILOT_LOG_FORMAT", "json")),
		ShutdownTimeout: envDuration("KUBEPILOT_SHUTDOWN_TIMEOUT", 15*time.Second),
	}
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
