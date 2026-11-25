// Package auth signs users in with Zitadel and keeps their tokens on the server;
// the browser gets only a session cookie.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/core"
)

const (
	SessionCookie   = "exampleweb_session"
	sessionLifetime = time.Hour
)

var ErrNoSession = errors.New("no session")

// Scopes asks for the organization claim, a refresh token, and two audiences:
// the platform API for the API Gateway and Zitadel's API for the user's memberships.
func Scopes(audience string) []string {
	return []string{
		oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeOfflineAccess,
		"urn:zitadel:iam:user:resourceowner",
		"urn:zitadel:iam:org:project:id:" + audience + ":aud",
		"urn:zitadel:iam:org:project:id:zitadel:aud",
	}
}

type Service struct {
	Provider  rp.RelyingParty
	Store     *Store
	PublicURL string
	ClientID  string
	Log       *slog.Logger
	Probe     func(ctx context.Context) error
	now       func() time.Time
}

func NewService(provider rp.RelyingParty, store *Store, publicURL, clientID string, log *slog.Logger) *Service {
	return &Service{Provider: provider, Store: store, PublicURL: publicURL, ClientID: clientID, Log: log, now: time.Now,
		Probe: func(ctx context.Context) error { return ProbeIssuer(ctx, provider.HttpClient(), provider.Issuer()) }}
}

func ProbeIssuer(ctx context.Context, client *http.Client, issuer string) error {
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

func Unavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "60")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(unavailablePage)
}

// NewCookies holds the short-lived OIDC state and PKCE verifier during sign-in,
// encrypted; the key is random per pod because they live only for one sign-in.
func NewCookies(secure bool) *httphelper.CookieHandler {
	hashKey := make([]byte, 32)
	encryptKey := make([]byte, 32)
	_, _ = rand.Read(hashKey)
	_, _ = rand.Read(encryptKey)
	opts := []httphelper.CookieHandlerOpt{httphelper.WithSameSite(http.SameSiteLaxMode)}
	if !secure {
		opts = append(opts, httphelper.WithUnsecure())
	}
	return httphelper.NewCookieHandler(hashKey, encryptKey, opts...)
}

// Zitadel may still be starting when the app does.
func NewProvider(ctx context.Context, log *slog.Logger, cfg core.Config, client *http.Client, cookies *httphelper.CookieHandler) (rp.RelyingParty, error) {
	for {
		provider, err := rp.NewRelyingPartyOIDC(ctx, cfg.Issuer, cfg.ClientID, cfg.ClientSecret,
			cfg.PublicURL+"/auth/callback", Scopes(cfg.Audience),
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

func (s *Service) secure() bool {
	return strings.HasPrefix(s.PublicURL, "https://")
}

// The login only redirects; probe Zitadel first so an outage gets our 503 page, not a dead redirect.
func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if s.Probe != nil {
		if err := s.Probe(r.Context()); err != nil {
			s.Log.WarnContext(r.Context(), "Zitadel not reachable, sign-in unavailable", "err", err)
			Unavailable(w)
			return
		}
	}
	rp.AuthURLHandler(randomState, s.Provider)(w, r)
}

func (s *Service) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	rp.CodeExchangeHandler(s.signIn, s.Provider)(w, r)
}

func (s *Service) signIn(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], _ string, _ rp.RelyingParty) {
	session := s.sessionFromTokens(tokens)
	if session.OrgID == "" {
		s.Log.ErrorContext(r.Context(), "token has no organization", "sub", session.Subject)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, s.Store.Create(session))
	s.Log.InfoContext(r.Context(), "signed in", "sub", session.Subject, "org", session.OrgID)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) sessionFromTokens(tokens *oidc.Tokens[*oidc.IDTokenClaims]) core.Session {
	session := core.Session{
		AccessToken:   tokens.AccessToken,
		RefreshToken:  tokens.RefreshToken,
		IDToken:       tokens.IDToken,
		AccessIssued:  s.now(),
		AccessExpires: tokens.Expiry,
		Expires:       s.now().Add(sessionLifetime),
	}
	if c := tokens.IDTokenClaims; c != nil {
		session.Subject = c.Subject
		session.Name = c.Name
		if session.Name == "" {
			session.Name = c.PreferredUsername
		}
		session.OrgID, _ = c.Claims[core.OrgClaim].(string)
	}
	return session
}

func (s *Service) setCookie(w http.ResponseWriter, id string) {
	//nolint:gosec // G124: Secure follows PUBLIC_URL; the kind prototype runs on plain HTTP.
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: s.secure(),
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLifetime.Seconds()),
	})
}

// Logout ends the session here and at Zitadel.
func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie(SessionCookie); err == nil {
		if session, ok := s.Store.Get(c.Value); ok {
			idToken = session.IDToken
		}
		s.Store.Delete(c.Value)
	}
	//nolint:gosec // G124: Secure follows PUBLIC_URL; the kind prototype runs on plain HTTP.
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secure(),
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
	target := "/"
	if endSession := s.Provider.GetEndSessionEndpoint(); endSession != "" {
		q := url.Values{"client_id": {s.ClientID}, "post_logout_redirect_uri": {s.PublicURL + "/"}}
		if idToken != "" {
			q.Set("id_token_hint", idToken)
		}
		target = endSession + "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// Session returns the session of the request and its ID.
func (s *Service) Session(r *http.Request) (string, core.Session, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return "", core.Session{}, ErrNoSession
	}
	session, ok := s.Store.Get(c.Value)
	if !ok {
		return "", core.Session{}, ErrNoSession
	}
	return c.Value, session, nil
}

// Refresh gets new tokens with the session's refresh token and stores them.
func (s *Service) Refresh(ctx context.Context, id string, session core.Session) (core.Session, error) {
	if session.RefreshToken == "" {
		return core.Session{}, errors.New("session has no refresh token")
	}
	tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, s.Provider, session.RefreshToken, "", "")
	if err != nil {
		return core.Session{}, err
	}
	session.AccessToken = tokens.AccessToken
	session.AccessIssued, session.AccessExpires = s.now(), tokens.Expiry
	if tokens.RefreshToken != "" {
		session.RefreshToken = tokens.RefreshToken
	}
	if tokens.IDToken != "" {
		session.IDToken = tokens.IDToken
	}
	s.Store.Update(id, session)
	return session, nil
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
