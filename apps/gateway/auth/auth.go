// Package auth requires the platform API audience, so a token for another app of the same
// Zitadel instance is rejected. Every organization except the Zitadel admin one is a tenant.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// Zitadel Organization ID of the token's user.
const TenantClaim = "urn:zitadel:iam:user:resourceowner:id"

var (
	ErrNoToken       = errors.New("missing bearer token")
	ErrNoTenant      = errors.New("token has no valid tenant claim")
	ErrWrongAudience = errors.New("token is not issued for the platform API")
	ErrNotTenant     = errors.New("token belongs to the Zitadel admin organization, not to a tenant")
	ErrTenantBlocked = errors.New("tenant is suspended or deleted")

	tenantRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type Verifier struct {
	verifier        *op.AccessTokenVerifier
	audience        string
	zitadelAdminOrg string
	// nil blocks none.
	Blocked func(tenant string) bool
}

// An unknown key ID triggers a JWKS refetch, so key rotation needs no restart.
func NewVerifier(issuer, audience, zitadelAdminOrg, jwksURL string, client *http.Client) *Verifier {
	keys := rp.NewRemoteKeySet(client, jwksURL)
	return &Verifier{verifier: op.NewAccessTokenVerifier(issuer, keys), audience: audience, zitadelAdminOrg: zitadelAdminOrg}
}

func (v *Verifier) Tenant(ctx context.Context, token string) (string, error) {
	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, token, v.verifier)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}
	// VerifyAccessToken does not check aud.
	if !slices.Contains(claims.Audience, v.audience) {
		return "", ErrWrongAudience
	}
	tenant, _ := claims.Claims[TenantClaim].(string)
	if !tenantRe.MatchString(tenant) {
		return "", ErrNoTenant
	}
	if tenant == v.zitadelAdminOrg {
		return "", ErrNotTenant
	}
	if v.Blocked != nil && v.Blocked(tenant) {
		return "", ErrTenantBlocked
	}
	return tenant, nil
}

func BearerToken(r *http.Request) (string, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", ErrNoToken
	}
	return token, nil
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
