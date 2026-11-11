// Package auth checks that a caller of Tenant Management holds the role
// tenant-manager. Callers are services, not people: the admin app gets an
// access token for its machine user with client_credentials, and Tenant
// Management validates it locally against Zitadel's JWKS (ADR-0003).
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// Role is the project role a caller needs for every route.
const Role = "tenant-manager"

var (
	ErrNoToken       = errors.New("missing bearer token")
	ErrWrongAudience = errors.New("token is not issued for Tenant Management")
	ErrNoRole        = errors.New("token lacks the role " + Role)
)

// RolesClaim is where Zitadel puts the roles a token holds in one project:
// a map from role key to the organizations that granted it.
func RolesClaim(projectID string) string {
	return "urn:zitadel:iam:org:project:" + projectID + ":roles"
}

// Verifier validates tokens of one issuer for one project.
type Verifier struct {
	verifier *op.AccessTokenVerifier
	audience string
}

// NewVerifier validates tokens of issuer that list audience (the project ID
// of Tenant Management) in aud. Keys come from jwksURL and are cached; an
// unknown key ID triggers a refetch, so key rotation needs no restart.
func NewVerifier(issuer, audience, jwksURL string, client *http.Client) *Verifier {
	keys := rp.NewRemoteKeySet(client, jwksURL)
	return &Verifier{verifier: op.NewAccessTokenVerifier(issuer, keys), audience: audience}
}

// Caller is an authenticated caller.
type Caller struct {
	Subject string // the Zitadel user ID of the calling service
}

// Check validates the token's issuer, signature, expiry and audience, and
// that it holds Role in the audience's project. It tells an invalid token
// (ErrUnauthenticated) from a valid one without the role (ErrNoRole).
func (v *Verifier) Check(ctx context.Context, token string) (Caller, error) {
	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, token, v.verifier)
	if err != nil {
		return Caller{}, fmt.Errorf("invalid token: %w", err)
	}
	// VerifyAccessToken does not check aud.
	if !slices.Contains(claims.Audience, v.audience) {
		return Caller{}, ErrWrongAudience
	}
	roles, _ := claims.Claims[RolesClaim(v.audience)].(map[string]any)
	if _, ok := roles[Role]; !ok {
		return Caller{Subject: claims.Subject}, ErrNoRole
	}
	return Caller{Subject: claims.Subject}, nil
}

type callerKey struct{}

// CallerFrom returns the caller that Require put into the context.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// Checker is what Require needs from a Verifier.
type Checker interface {
	Check(ctx context.Context, token string) (Caller, error)
}

// Require answers 401 without a valid token and 403 for a valid token
// without the role; next runs only for a caller with the role.
func Require(v Checker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := BearerToken(r)
		if err != nil {
			deny(w, http.StatusUnauthorized, "missing or malformed bearer token")
			return
		}
		caller, err := v.Check(r.Context(), token)
		switch {
		case errors.Is(err, ErrNoRole):
			deny(w, http.StatusForbidden, "the caller lacks the role "+Role)
			return
		case err != nil:
			deny(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="tenant-management"`)
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "{\"error\":%q}\n", msg)
}

// BearerToken returns the token from an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) (string, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

// NewIssuerClient talks to Zitadel. With dialAddr set, connections to the
// issuer's host go to dialAddr instead: inside the cluster the public issuer
// name does not resolve, but Zitadel must still see it as the Host header.
func NewIssuerClient(issuer, dialAddr string, timeout time.Duration) (*http.Client, error) {
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
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}
