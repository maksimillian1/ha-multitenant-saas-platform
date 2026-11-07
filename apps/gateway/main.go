// Command gateway is the API Gateway (ADR-0004). It sits behind the
// infrastructure entry point (Cilium Gateway API), validates the Zitadel JWT,
// sets x-tenant-id and routes by path prefix to the platform services.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/proxy"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/usage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := core.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	proxyService, usageService, healthService, err := bootstrapServices(cfg, log)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.Handler)
	mux.Handle("/", core.AccessLog(http.HandlerFunc(proxyService.Handler), log))

	metrics := http.NewServeMux()
	if usageService != nil {
		metrics.HandleFunc("GET /metrics", usageService.MetricsHandler)
	} else {
		metrics.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {})
	}
	go func() {
		if err := core.Serve(ctx, log, cfg.MetricsAddr, metrics); err != nil {
			log.Error("metrics listener", "err", err)
		}
	}()

	err = core.Serve(ctx, log, cfg.ListenAddr, mux)
	if usageService != nil {
		// Events still retrying are given up after this and counted as expired.
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		usageService.Close(closeCtx)
		cancel()
	}
	if err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

// bootstrapServices returns a nil usage service when USAGE_URL is not set.
func bootstrapServices(cfg core.Config, log *slog.Logger) (*proxy.Service, *usage.Service, *health.Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, err
	}
	routes, err := proxy.ParseRoutes(cfg.Routes)
	if err != nil {
		return nil, nil, nil, err
	}
	issuerClient, err := auth.NewIssuerClient(cfg.Issuer, cfg.IssuerDialAddr)
	if err != nil {
		return nil, nil, nil, err
	}
	verifier := auth.NewVerifier(cfg.Issuer, cfg.Audience, cfg.JWKSURL, issuerClient)

	var reporter core.UsageReporter
	var usageService *usage.Service
	if cfg.UsageURL != "" {
		usageService = usage.NewService(cfg.UsageURL, cfg.UsageMaxPending, log)
		reporter = usageService
	} else {
		log.Warn("USAGE_URL is not set: API calls are not reported as usage events")
	}

	proxyService := proxy.NewService(verifier, routes, reporter, log)
	healthService := health.NewService()

	return proxyService, usageService, healthService, nil
}
