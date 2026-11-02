// Package auth signs the super admin in with Zitadel: OIDC authorization code
// flow with PKCE, then an encrypted session cookie. The page itself decides
// who may see it (packages api and ui); auth only says who is signed in.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	now       func() time.Time
}

var _ core.SessionReader = (*Service)(nil)

func NewService(provider rp.RelyingParty, cookies *httphelper.CookieHandler, publicURL, clientID string, log *slog.Logger) *Service {
	return &Service{Provider: provider, Cookies: cookies, PublicURL: publicURL, ClientID: clientID, Log: log, now: time.Now}
}

// NewCookies encrypts and signs the state, PKCE and session cookies with keys
// derived from sessionKey. The cookies are not Secure: the prototype runs on
// plain HTTP.
func NewCookies(sessionKey string) *httphelper.CookieHandler {
	hashKey := sha256.Sum256([]byte("hash:" + sessionKey))
	encryptKey := sha256.Sum256([]byte("encrypt:" + sessionKey))
	return httphelper.NewCookieHandler(hashKey[:], encryptKey[:], httphelper.WithUnsecure())
}

// NewProvider discovers the issuer and retries until ctx is done, because
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

func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
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

// LogoutHandler ends the session here and at Zitadel, then comes back to /.
func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	s.Cookies.DeleteCookie(w, sessionCookie)
	target := "/"
	if endSession := s.Provider.GetEndSessionEndpoint(); endSession != "" {
		q := url.Values{"client_id": {s.ClientID}, "post_logout_redirect_uri": {s.PublicURL + "/"}}
		target = endSession + "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// Session returns the session of r if its cookie is intact and not expired.
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

// SessionFromClaims is the session of a verified ID token.
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

// NewIssuerClient talks to Zitadel. With dialAddr set, connections to the
// issuer's host go to dialAddr instead: inside the cluster the public issuer
// name does not resolve, but Zitadel must still see it as the Host header.
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
