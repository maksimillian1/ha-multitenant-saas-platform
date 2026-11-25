// Package auth only says who is signed in; packages api and ui decide who may see what.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

const (
	sessionCookie   = "admin_session"
	sessionLifetime = time.Hour
)

var scopes = []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail}

type Service struct {
	Provider  rp.RelyingParty
	Cookies   *httphelper.CookieHandler
	PublicURL string
	ClientID  string
	Log       *slog.Logger
	Probe     func(ctx context.Context) error
	now       func() time.Time
}

var _ core.SessionReader = (*Service)(nil)

func NewService(provider rp.RelyingParty, cookies *httphelper.CookieHandler, publicURL, clientID string, log *slog.Logger) *Service {
	return &Service{Provider: provider, Cookies: cookies, PublicURL: publicURL, ClientID: clientID, Log: log, now: time.Now,
		Probe: func(ctx context.Context) error { return probeIssuer(ctx, provider.HttpClient(), provider.Issuer()) }}
}

func probeIssuer(ctx context.Context, client *http.Client, issuer string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("issuer: status %d", resp.StatusCode)
	}
	return nil
}

var unavailablePage = []byte(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Sign-in unavailable</title></head>
<body><main id="sign-in-unavailable"><h1>Sign-in is temporarily unavailable</h1><p>Please try again in a few minutes.</p></main></body>
</html>
`)

// Not Secure: the prototype runs on plain HTTP.
func NewCookies(sessionKey string) *httphelper.CookieHandler {
	hashKey := sha256.Sum256([]byte("hash:" + sessionKey))
	encryptKey := sha256.Sum256([]byte("encrypt:" + sessionKey))
	return httphelper.NewCookieHandler(hashKey[:], encryptKey[:], httphelper.WithUnsecure())
}

// Zitadel may still be starting when the app does.
func NewProvider(ctx context.Context, log *slog.Logger, cfg core.Config, client *http.Client, cookies *httphelper.CookieHandler) (rp.RelyingParty, error) {
	for {
		provider, err := rp.NewRelyingPartyOIDC(ctx, cfg.Issuer, cfg.ClientID, cfg.ClientSecret,
			cfg.PublicURL+"/auth/callback", scopes,
			rp.WithPKCE(cookies), rp.WithCookieHandler(cookies), rp.WithHTTPClient(client),
			rp.WithVerifierOpts(rp.WithIssuedAtOffset(5*time.Second)))
		if err == nil {
			return provider, nil
		}
		log.Warn("issuer not reachable, retrying", "issuer", cfg.Issuer, "err", err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(5 * time.Second):
		}
	}
}

// The login only redirects; probe Zitadel first so an outage gets our 503 page, not a dead redirect.
func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if s.Probe != nil {
		if err := s.Probe(r.Context()); err != nil {
			s.Log.WarnContext(r.Context(), "Zitadel not reachable, sign-in unavailable", "err", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write(unavailablePage)
			return
		}
	}
	rp.AuthURLHandler(randomState, s.Provider)(w, r)
}

func (s *Service) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	rp.CodeExchangeHandler(s.signIn, s.Provider)(w, r)
}

func (s *Service) signIn(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], _ string, _ rp.RelyingParty) {
	session := SessionFromClaims(tokens.IDTokenClaims, s.now())
	if err := s.setSession(w, session); err != nil {
		s.Log.ErrorContext(r.Context(), "session not stored", "err", err)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
		return
	}
	s.Log.InfoContext(r.Context(), "signed in", "sub", session.Subject, "roles", session.Roles)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	s.Cookies.DeleteCookie(w, sessionCookie)
	target := "/"
	if endSession := s.Provider.GetEndSessionEndpoint(); endSession != "" {
		q := url.Values{"client_id": {s.ClientID}, "post_logout_redirect_uri": {s.PublicURL + "/"}}
		target = endSession + "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Service) Session(r *http.Request) (core.Session, bool) {
	raw, err := s.Cookies.CheckCookie(r, sessionCookie)
	if err != nil {
		return core.Session{}, false
	}
	var session core.Session
	if err := json.Unmarshal([]byte(raw), &session); err != nil || !s.now().Before(session.Expires) {
		return core.Session{}, false
	}
	return session, true
}

func (s *Service) setSession(w http.ResponseWriter, session core.Session) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return s.Cookies.SetCookie(w, sessionCookie, string(raw))
}

func SessionFromClaims(claims *oidc.IDTokenClaims, now time.Time) core.Session {
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	if name == "" {
		name = claims.Subject
	}
	var roles []string
	if granted, ok := claims.Claims[core.RolesClaim].(map[string]any); ok {
		for role := range granted {
			roles = append(roles, role)
		}
		slices.Sort(roles)
	}
	return core.Session{
		Subject: claims.Subject,
		Name:    name,
		Email:   claims.Email,
		Roles:   roles,
		Expires: now.Add(sessionLifetime),
	}
}

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Inside the cluster the public issuer name does not resolve, but Zitadel must still see it as Host.
func NewIssuerClient(issuer, dialAddr string) (*http.Client, error) {
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
