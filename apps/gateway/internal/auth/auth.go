// Package auth validates Zitadel access tokens locally against the issuer's
// JWKS (ADR-0003) and returns the caller's tenant: the Organization ID claim.
// A token must carry the platform API's audience, so a token issued for
// another application of the same Zitadel instance is rejected (HAMSP-22).
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// TenantClaim holds the Zitadel Organization ID of the token's user.
const TenantClaim = "urn:zitadel:iam:user:resourceowner:id"

var (
	ErrNoToken       = errors.New("missing bearer token")
	ErrNoTenant      = errors.New("token has no valid tenant claim")
	ErrWrongAudience = errors.New("token is not issued for the platform API")

	tenantRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type Verifier struct {
	verifier *op.AccessTokenVerifier
	audience string
}

// NewVerifier validates tokens of issuer that list audience in aud. Keys come
// from jwksURL and are cached; an unknown key ID triggers a refetch, so
// Zitadel's key rotation needs no restart.
func NewVerifier(issuer, audience, jwksURL string, client *http.Client) *Verifier {
	keys := rp.NewRemoteKeySet(client, jwksURL)
	return &Verifier{verifier: op.NewAccessTokenVerifier(issuer, keys), audience: audience}
}

// Tenant checks the token's issuer, signature, expiry and audience and returns
// its tenant.
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
	return tenant, nil
}

// BearerToken returns the token from an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) (string, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", ErrNoToken
	}
	return token, nil
}
