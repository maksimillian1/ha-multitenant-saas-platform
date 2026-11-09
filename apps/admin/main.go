// Command admin is the platform's super admin app: a Go backend for frontend
// that serves the React SPA (ui/app) and its JSON API (openapi.yaml) to users
// who sign in with Zitadel; the API answers only to the super-admin role of the
// platform-admin project. It does its own OIDC sign-in and keeps the tokens on
// the server; the browser holds only an encrypted session cookie. Its public
// route does not go through the API Gateway, which accepts only bearer tokens.
//
// The API manages Zitadel's organizations, users, role assignments and
// branding as the app's own service account admin-app, never with the
// admin's token. It gets short-lived access tokens with the account's JWT
// profile key (ZITADEL_KEY_FILE).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/api"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/ui"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
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
	apiService.Register(mux)
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
	// The issuer client also reaches the Zitadel APIs and the token
	// endpoint: same host, same in-cluster dial address.
	key, err := zitadel.LoadKey("ZITADEL_KEY_FILE", cfg.ZitadelKeyFile)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tokens, err := zitadel.NewKeyTokens(ctx, cfg.Issuer, key, issuerClient)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	apiService := newAPIService(cfg, authService, tokens, issuerClient, log)
	// Both service accounts are checked for credentials they should not
	// have (a PAT, a second key, a long key); a finding is an ERROR event.
	drift := &zitadel.DriftCheck{
		Client: zitadel.NewClient(cfg.ZitadelURL, tokens, issuerClient),
		Accounts: []zitadel.Account{
			{Name: "admin-app", UserID: key.UserID, OwnKeyID: key.KeyID},
			{Name: "iam-admin"},
		},
		Log: log,
	}
	go drift.Loop(ctx, driftInterval)
	uiService := ui.NewService(authService, log)
	healthService := health.NewService()

	return authService, apiService, uiService, healthService, nil
}

// driftInterval is how often the service accounts' credentials are checked.
const driftInterval = 5 * time.Minute

// newAPIService builds the API with one Zitadel client, the admin app's
// account, for every route; the platform branding uses it too (HAMSP-36).
func newAPIService(cfg core.Config, sessions core.SessionReader, tokens zitadel.Tokens, httpClient *http.Client, log *slog.Logger) *api.Service {
	client := zitadel.NewClient(cfg.ZitadelURL, tokens, httpClient)
	return api.NewService(sessions, client, client, cfg.AdminRole, cfg.PublicURL, log)
}
