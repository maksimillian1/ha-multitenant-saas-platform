package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const issuer = "http://zitadel.test"

func newKey(t *testing.T, kid string) jose.JSONWebKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return jose.JSONWebKey{Key: k, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"}
}

func sign(t *testing.T, key jose.JSONWebKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// newTestVerifier serves the public part of key as the issuer's JWKS.
func newTestVerifier(t *testing.T, key jose.JSONWebKey) *Verifier {
	t.Helper()
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key.Public()}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return NewVerifier(issuer, srv.URL, srv.Client())
}

func claims(mod func(map[string]any)) map[string]any {
	now := time.Now()
	c := map[string]any{
		"iss": issuer, "sub": "user-1", "aud": []string{"client-1"},
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		TenantClaim: "396438053259838069",
	}
	if mod != nil {
		mod(c)
	}
	return c
}

func TestValidTokenReturnsOrganizationID(t *testing.T) {
	key := newKey(t, "k1")
	tenant, err := newTestVerifier(t, key).Tenant(context.Background(), sign(t, key, claims(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if tenant != "396438053259838069" {
		t.Fatalf("tenant = %q", tenant)
	}
}

func TestInvalidTokensAreRejected(t *testing.T) {
	key := newKey(t, "k1")
	v := newTestVerifier(t, key)
	valid := sign(t, key, claims(nil))
	parts := strings.Split(valid, ".")
	tampered := parts[0] + "." + strings.Split(sign(t, key, claims(func(c map[string]any) { c[TenantClaim] = "other" })), ".")[1] + "." + parts[2]

	cases := map[string]string{
		"garbage":       "not-a-jwt",
		"bad signature": tampered,
		"foreign key":   sign(t, newKey(t, "k1"), claims(nil)),
		"unknown kid":   sign(t, newKey(t, "k2"), claims(nil)),
		"other issuer":  sign(t, key, claims(func(c map[string]any) { c["iss"] = "http://evil.test" })),
		"expired":       sign(t, key, claims(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() })),
		"no tenant":     sign(t, key, claims(func(c map[string]any) { delete(c, TenantClaim) })),
		"bad tenant":    sign(t, key, claims(func(c map[string]any) { c[TenantClaim] = "a b" })),
		"tenant number": sign(t, key, claims(func(c map[string]any) { c[TenantClaim] = 42 })),
	}
	for name, tok := range cases {
		if tenant, err := v.Tenant(context.Background(), tok); err == nil {
			t.Errorf("%s: accepted, tenant %q", name, tenant)
		}
	}
}

func TestBearerToken(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer abc": "abc",
		"bearer abc": "abc",
		"":           "",
		"Basic abc":  "",
		"Bearer ":    "",
		"Bearer":     "",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		got, err := BearerToken(r)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("%q: got %q, %v", header, got, err)
		}
	}
}
