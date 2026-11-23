// Command exampleweb is the tenant's web page for ExampleApp: it signs users in
// with Zitadel, keeps their tokens on the server and calls ExampleApp through
// the API Gateway with them. Its own route bypasses the API Gateway, which
// accepts only bearer tokens.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/items"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/page"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := core.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cfg.Validate(); err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}
	issuerClient, err := auth.NewIssuerClient(cfg.Issuer, cfg.IssuerDialAddr)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}
	cookies := auth.NewCookies(strings.HasPrefix(cfg.PublicURL, "https://"))
	provider, err := auth.NewProvider(ctx, log, cfg, issuerClient, cookies)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}
	authService := auth.NewService(provider, auth.NewStore(), cfg.PublicURL, cfg.ClientID, log)
	pageService := &page.Service{
		Sessions: authService,
		Items:    &items.Client{GatewayURL: cfg.GatewayURL, HTTP: &http.Client{Timeout: 10 * time.Second}},
		// The issuer client reaches Zitadel's API too: same host, same dial address.
		Owners:     &page.Memberships{ZitadelURL: cfg.ZitadelURL, HTTP: issuerClient},
		ConsoleURL: cfg.ConsoleURL,
		Log:        log,
	}

	if err := core.Serve(ctx, log, cfg.ListenAddr, core.AccessLog(routes(authService, pageService, health.NewService()), log)); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func routes(authService *auth.Service, pageService *page.Service, healthService *health.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.Handler)
	mux.HandleFunc("GET /auth/login", authService.LoginHandler)
	mux.HandleFunc("GET /auth/callback", authService.CallbackHandler)
	mux.HandleFunc("POST /auth/logout", authService.LogoutHandler)
	mux.HandleFunc("GET /{$}", pageService.Handler)
	return mux
}
