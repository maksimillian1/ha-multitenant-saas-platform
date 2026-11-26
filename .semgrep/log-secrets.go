package semgrep

import (
	"log"
	"log/slog"
	"net/http"
)

// Verifies: guardrails AC-15
func logSecretsCases(r *http.Request, logger *slog.Logger, accessToken, clientSecret, tenantID string) {
	// ruleid: log-secrets
	slog.Info("issued", "token", accessToken)
	// ruleid: log-secrets
	logger.Error("bad client", "client", clientSecret)
	// ruleid: log-secrets
	slog.Debug("request", "auth", r.Header.Get("Authorization"))
	// ruleid: log-secrets
	log.Printf("password=%s", accessToken)
	// ruleid: log-secrets
	logger.Info("login", slog.String("password", tenantID))

	// ok: log-secrets
	slog.Info("issued", "tenant_id", tenantID)
	// ok: log-secrets
	logger.Warn("token refresh failed", "err", http.ErrHandlerTimeout)
	// ok: log-secrets
	slog.Info("request", "method", r.Method, "path", r.URL.Path)
}
