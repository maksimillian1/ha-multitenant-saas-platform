// Command admin is the platform's super admin app: a Go backend for frontend
// that serves the React SPA (ui/app) and its JSON API (openapi.yaml) to users
// who sign in with Zitadel; the API answers only to the super-admin role of the
// platform-admin project. It does its own OIDC sign-in and keeps the tokens on
// the server; the browser holds only an encrypted session cookie. Its public
// route does not go through the API Gateway, which accepts only bearer tokens.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/api"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/ui"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := core.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	authService, apiService, uiService, healthService, err := bootstrapServices(ctx, cfg, log)
	if err != nil {
		log.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}

	mux := routes(authService, apiService, uiService, healthService)
	if err := core.Serve(ctx, log, cfg.ListenAddr, core.AccessLog(mux, log)); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func routes(authService *auth.Service, apiService *api.Service, uiService *ui.Service, healthService *health.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.Handler)
	mux.HandleFunc("GET /auth/login", authService.LoginHandler)
	mux.HandleFunc("GET /auth/callback", authService.CallbackHandler)
	mux.HandleFunc("POST /auth/logout", authService.LogoutHandler)
	mux.HandleFunc("GET /api/me", apiService.MeHandler)
	mux.HandleFunc("GET /api/", apiService.NotFoundHandler)
	// Every other path belongs to the SPA's router.
	mux.HandleFunc("GET /", uiService.Handler)
	return mux
}

func bootstrapServices(ctx context.Context, cfg core.Config, log *slog.Logger) (*auth.Service, *api.Service, *ui.Service, *health.Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, nil, err
	}
	issuerClient, err := auth.NewIssuerClient(cfg.Issuer, cfg.IssuerDialAddr)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cookies := auth.NewCookies(cfg.SessionKey)
	provider, err := auth.NewProvider(ctx, log, cfg, issuerClient, cookies)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	authService := auth.NewService(provider, cookies, cfg.PublicURL, cfg.ClientID, log)
	apiService := api.NewService(authService, cfg.AdminRole, log)
	uiService := ui.NewService(authService, log)
	healthService := health.NewService()

	return authService, apiService, uiService, healthService, nil
}
