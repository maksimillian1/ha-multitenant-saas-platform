// Command gateway is the API Gateway (ADR-0004). It sits behind the
// infrastructure entry point (Cilium Gateway API), validates the Zitadel JWT,
// sets x-tenant-id and routes by path prefix to the platform services.
//
// Configuration (environment):
//
//	ISSUER            token issuer, e.g. http://zitadel.localtest.me:30080 (required)
//	AUDIENCE          audience a token must carry: the Zitadel project ID of the
//	                  platform API (required)
//	JWKS_URL          default ISSUER + /oauth/v2/keys
//	ISSUER_DIAL_ADDR  host:port to connect to for the issuer's URLs, e.g.
//	                  zitadel.zitadel.svc:8080; the Host header stays the issuer's
//	ROUTES            /prefix=http://upstream,... (required)
//	LISTEN_ADDR       default :8080
//	USAGE_URL         UsageWriteAPI, e.g. http://usage-writeapi.usage.svc:8080/v1/events;
//	                  empty disables usage reporting (HAMSP-26)
//	USAGE_MAX_PENDING unsent usage events at most, default 10000
//	METRICS_ADDR      metrics listener, default :9090; not routed by the public Gateway
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/internal/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/internal/proxy"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/internal/usage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(log *slog.Logger) error {
	issuer := os.Getenv("ISSUER")
	if issuer == "" {
		return errors.New("ISSUER is required")
	}
	audience := os.Getenv("AUDIENCE")
	if audience == "" {
		return errors.New("AUDIENCE is required")
	}
	routes, err := proxy.ParseRoutes(os.Getenv("ROUTES"))
	if err != nil {
		return err
	}
	issuerClient, err := issuerHTTPClient(issuer, os.Getenv("ISSUER_DIAL_ADDR"))
	if err != nil {
		return err
	}
	verifier := auth.NewVerifier(issuer, audience, env("JWKS_URL", issuer+"/oauth/v2/keys"), issuerClient)

	var reporter proxy.UsageReporter
	var usageReporter *usage.Reporter
	if u := os.Getenv("USAGE_URL"); u != "" {
		limit, err := strconv.Atoi(env("USAGE_MAX_PENDING", "10000"))
		if err != nil || limit <= 0 {
			return errors.New("USAGE_MAX_PENDING must be a positive integer")
		}
		usageReporter = usage.New(usage.Config{URL: u, Limit: limit}, log)
		reporter = usageReporter
	} else {
		log.Warn("USAGE_URL is not set: API calls are not reported as usage events")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/", accessLog(proxy.New(verifier, routes, reporter, log), log))

	metrics := http.NewServeMux()
	metrics.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if usageReporter != nil {
			usageReporter.WriteMetrics(w)
		}
	})
	metricsSrv := &http.Server{Addr: env("METRICS_ADDR", ":9090"), Handler: metrics, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics listener", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              env("LISTEN_ADDR", ":8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = metricsSrv.Shutdown(shutdownCtx)
		if usageReporter != nil {
			// Events still retrying are given up after this and counted as expired.
			usageReporter.Close(shutdownCtx)
		}
	}()
	log.Info("listening", "addr", srv.Addr, "routes", len(routes))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// issuerHTTPClient fetches the JWKS. With dialAddr set, connections to the
// issuer's host go to dialAddr instead: inside the cluster the public issuer
// name does not resolve, but Zitadel must still see it as the Host header.
func issuerHTTPClient(issuer, dialAddr string) (*http.Client, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return nil, err
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	issuerAddr := net.JoinHostPort(u.Hostname(), port)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if dialAddr != "" && addr == issuerAddr {
			addr = dialAddr
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}, nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func accessLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.InfoContext(r.Context(), "request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"tenant_id", r.Header.Get(proxy.TenantHeader), "duration_ms", time.Since(start).Milliseconds())
	})
}
