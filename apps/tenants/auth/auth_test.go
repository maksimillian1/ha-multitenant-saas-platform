package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	issuer  = "http://zitadel.test"
	project = "tenant-management-project"
)

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
	return NewVerifier(issuer, project, srv.URL, srv.Client())
}

func claims(mod func(map[string]any)) map[string]any {
	now := time.Now()
	c := map[string]any{
		"iss": issuer, "sub": "admin-app-user", "aud": []string{"client-1", project},
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		RolesClaim(project): map[string]any{Role: map[string]any{"platform-org": "platform.test"}},
	}
	if mod != nil {
		mod(c)
	}
	return c
}

// Verifies: tenant-management AC-9; T-04, T-12
func TestTokenChecks(t *testing.T) {
	key := newKey(t, "k1")
	other := newKey(t, "k1") // same key ID, different key: a forged signature
	v := newTestVerifier(t, key)

	unsigned := func() string {
		head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		body, _ := json.Marshal(claims(nil))
		return head + "." + base64.RawURLEncoding.EncodeToString(body) + "."
	}

	cases := []struct {
		name    string
		token   string
		wantErr error // nil: valid; ErrNoRole: 403; any other: 401
		invalid bool
	}{
		{name: "valid with role", token: sign(t, key, claims(nil))},
		{name: "no roles claim", token: sign(t, key, claims(func(c map[string]any) { delete(c, RolesClaim(project)) })), wantErr: ErrNoRole},
		{name: "other role only", token: sign(t, key, claims(func(c map[string]any) {
			c[RolesClaim(project)] = map[string]any{"viewer": map[string]any{}}
		})), wantErr: ErrNoRole},
		{name: "role in another project", token: sign(t, key, claims(func(c map[string]any) {
			delete(c, RolesClaim(project))
			c[RolesClaim("other-project")] = map[string]any{Role: map[string]any{}}
		})), wantErr: ErrNoRole},
		{name: "wrong audience", token: sign(t, key, claims(func(c map[string]any) { c["aud"] = []string{"other-project"} })), invalid: true},
		{name: "wrong issuer", token: sign(t, key, claims(func(c map[string]any) { c["iss"] = "http://evil.test" })), invalid: true},
		{name: "expired", token: sign(t, key, claims(func(c map[string]any) {
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			c["iat"] = time.Now().Add(-2 * time.Hour).Unix()
		})), invalid: true},
		{name: "forged signature", token: sign(t, other, claims(nil)), invalid: true},
		{name: "alg none", token: unsigned(), invalid: true},
		{name: "garbage", token: "not-a-jwt", invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller, err := v.Check(context.Background(), tc.token)
			switch {
			case tc.invalid:
				if err == nil || errors.Is(err, ErrNoRole) {
					t.Fatalf("err = %v, want an invalid-token error", err)
				}
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if caller.Subject != "admin-app-user" {
					t.Fatalf("subject = %q", caller.Subject)
				}
			}
		})
	}
}

// Verifies: tenant-management AC-9; T-04
func TestRequire(t *testing.T) {
	key := newKey(t, "k1")
	v := newTestVerifier(t, key)
	reached := false
	h := Require(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if c, ok := CallerFrom(r.Context()); !ok || c.Subject == "" {
			t.Error("no caller in the context")
		}
	}))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"basic scheme", "Basic abc", http.StatusUnauthorized},
		{"invalid token", "Bearer garbage", http.StatusUnauthorized},
		{"no role", "Bearer " + sign(t, key, claims(func(c map[string]any) { delete(c, RolesClaim(project)) })), http.StatusForbidden},
		{"role", "Bearer " + sign(t, key, claims(nil)), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodGet, "/tenants", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if reached != (tc.want == http.StatusOK) {
				t.Fatalf("handler reached = %v", reached)
			}
			if tc.want != http.StatusOK && !strings.Contains(rec.Body.String(), "error") {
				t.Fatalf("body = %s", rec.Body)
			}
		})
	}
}
