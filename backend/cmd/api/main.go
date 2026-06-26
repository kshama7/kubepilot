// Command api is the KubePilot backend HTTP server.
//
// It boots a chi router exposing health, metrics, and the cluster-health
// analysis endpoint, with structured logging, Prometheus instrumentation, and
// graceful shutdown. It starts even without a reachable cluster: analysis
// endpoints degrade to 503 rather than preventing the process from coming up.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/kshama7/kubepilot/backend/internal/ai"
	"github.com/kshama7/kubepilot/backend/internal/api"
	"github.com/kshama7/kubepilot/backend/internal/config"
	"github.com/kshama7/kubepilot/backend/internal/k8s"
	"github.com/kshama7/kubepilot/backend/internal/metrics"
	"github.com/kshama7/kubepilot/backend/internal/observability"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// --healthcheck is a self-probe used by the container HEALTHCHECK so a
	// distroless image needs no curl/wget. It hits the local /healthz and exits
	// 0 on 2xx, 1 otherwise.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.Parse()
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// runHealthcheck probes the local server's /healthz and returns a process exit
// code. The address mirrors the server bind address (KUBEPILOT_ADDR).
func runHealthcheck() int {
	cfg := config.Load()
	host := cfg.Addr
	if len(host) > 0 && host[0] == ':' {
		host = "127.0.0.1" + host
	}
	url := "http://" + host + "/healthz"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func run() error {
	cfg := config.Load()

	logger, err := newLogger(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("starting kubepilot api",
		zap.String("version", version),
		zap.String("addr", cfg.Addr),
	)

	// SIGINT/SIGTERM cancels this context, triggering graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional distributed tracing. No-op when KUBEPILOT_OTEL_ENDPOINT is unset.
	shutdownTracer, err := observability.InitTracer(ctx, observability.Config{
		Endpoint:    cfg.Tracing.Endpoint,
		Insecure:    cfg.Tracing.Insecure,
		ServiceName: "kubepilot-api",
		Version:     version,
	})
	if err != nil {
		logger.Warn("tracing disabled: failed to initialize exporter", zap.Error(err))
	} else if cfg.Tracing.Endpoint != "" {
		logger.Info("tracing enabled", zap.String("otlp_endpoint", cfg.Tracing.Endpoint))
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracer(shutdownCtx)
	}()

	m := metrics.New()

	// A missing/unreachable cluster at boot is non-fatal: the API still serves
	// /healthz and /metrics, and analysis endpoints return 503 until a cluster
	// is configured.
	var collector api.ClusterCollector
	promOpts := k8s.PrometheusOptions{
		URL:           cfg.Prometheus.URL,
		LookbackHours: cfg.Prometheus.LookbackHours,
		StepMinutes:   cfg.Prometheus.StepMinutes,
		CPUQuery:      cfg.Prometheus.CPUQuery,
		MemQuery:      cfg.Prometheus.MemQuery,
	}
	if client, err := k8s.NewClient(logger.Named("k8s"), cfg.Kubeconfig, promOpts); err != nil {
		logger.Warn("kubernetes client unavailable; analysis endpoints will return 503",
			zap.Error(err))
	} else {
		collector = client
	}

	explainer := ai.NewExplainer(ai.Config{
		APIKey:    cfg.AI.APIKey,
		Model:     cfg.AI.Model,
		MaxTokens: cfg.AI.MaxTokens,
	})
	if explainer.Enabled() {
		logger.Info("ai explanation layer enabled", zap.String("model", explainer.Model()))
	} else {
		logger.Info("ai explanation layer disabled; set KUBEPILOT_ANTHROPIC_API_KEY to enable")
	}

	srv := api.NewServer(logger.Named("http"), m, collector, explainer, version)
	// otelhttp turns every request — i.e. every analysis run — into a traceable
	// span; the API middleware enriches it with route/cluster/analyzer attributes.
	handler := otelhttp.NewHandler(srv.Router(), "kubepilot-api")
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", zap.String("addr", cfg.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections",
			zap.Duration("timeout", cfg.ShutdownTimeout))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

// newLogger builds a Zap logger at the given level and format (json|console).
func newLogger(level, format string) (*zap.Logger, error) {
	var zcfg zap.Config
	if format == "console" {
		zcfg = zap.NewDevelopmentConfig()
	} else {
		zcfg = zap.NewProductionConfig()
	}
	lvl, err := zap.ParseAtomicLevel(level)
	if err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", level, err)
	}
	zcfg.Level = lvl
	return zcfg.Build()
}
