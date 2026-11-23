// Package auth: callers are services with client_credentials tokens, validated locally (ADR-0003).
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

const Role = "tenant-manager"

var (
	ErrNoToken       = errors.New("missing bearer token")
	ErrWrongAudience = errors.New("token is not issued for Tenant Management")
	ErrNoRole        = errors.New("token lacks the role " + Role)
)

// Zitadel maps role key to the organizations that granted it.
func RolesClaim(projectID string) string {
	return "urn:zitadel:iam:org:project:" + projectID + ":roles"
}

type Verifier struct {
	verifier *op.AccessTokenVerifier
	audience string
}

// An unknown key ID triggers a JWKS refetch, so key rotation needs no restart.
func NewVerifier(issuer, audience, jwksURL string, client *http.Client) *Verifier {
	keys := rp.NewRemoteKeySet(client, jwksURL)
	return &Verifier{verifier: op.NewAccessTokenVerifier(issuer, keys), audience: audience}
}

type Caller struct {
	Subject string // Zitadel user ID of the calling service
}

// ErrUnauthenticated for an invalid token, ErrNoRole for a valid one without the role.
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

func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

type Checker interface {
	Check(ctx context.Context, token string) (Caller, error)
}

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

func BearerToken(r *http.Request) (string, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

// Inside the cluster the public issuer name does not resolve, but Zitadel must still see it as Host.
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
