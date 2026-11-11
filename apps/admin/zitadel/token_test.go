package zitadel

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// staticTokens is a fixed access token for tests of the API calls.
type staticTokens string

func (s staticTokens) Token(context.Context) (string, error) { return string(s), nil }
func (staticTokens) Invalidate(string)                       {}

// testKey is a JWT profile key made for one test.
func testKey(t *testing.T) (Key, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return Key{Type: "serviceaccount", KeyID: "key-1", Key: string(pemKey), UserID: "user-admin-app"}, priv
}

// tokenZitadel is a Zitadel with a token endpoint that checks the JWT bearer
// grant and an API that accepts only the tokens it issued and still allows.
type tokenZitadel struct {
	t       *testing.T
	pub     *rsa.PublicKey
	secrets []string // what must never appear in a request
	expires int      // expires_in of every token, seconds

	mu        sync.Mutex
	issued    int
	accepted  map[string]bool
	refuseAll bool // refuse every token, also new ones
	apiCalls  int
	apiBodies []string // the body of every API call
}

func (z *tokenZitadel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	for _, s := range z.secrets {
		if strings.Contains(string(raw), s) || strings.Contains(fmt.Sprint(r.Header), s) || strings.Contains(r.URL.String(), s) {
			z.t.Errorf("%s %s carries the private key", r.Method, r.URL.Path)
		}
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if r.URL.Path == "/oauth/v2/token" {
		z.token(w, string(raw))
		return
	}
	z.apiCalls++
	z.apiBodies = append(z.apiBodies, string(raw))
	auth := r.Header.Get("Authorization")
	if z.refuseAll || !strings.HasPrefix(auth, "Bearer ") || !z.accepted[strings.TrimPrefix(auth, "Bearer ")] {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"token invalid"}`))
		return
	}
	_, _ = w.Write([]byte(`{"user":{"id":"user-admin-app","details":{"resourceOwner":"org-platform"}}}`))
}

func (z *tokenZitadel) token(w http.ResponseWriter, body string) {
	form, err := parseForm(body)
	if err != nil || form["grant_type"] != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
		return
	}
	if !strings.Contains(form["scope"], zitadelAPIScope) {
		z.t.Errorf("scope %q has no %s", form["scope"], zitadelAPIScope)
	}
	if err := z.checkAssertion(form["assertion"]); err != nil {
		z.t.Errorf("assertion: %v", err)
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	z.issued++
	tok := fmt.Sprintf("access-%d", z.issued)
	z.accepted[tok] = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": z.expires})
}

// checkAssertion verifies the RS256 signature and the claims of the JWT.
func (z *tokenZitadel) checkAssertion(jwt string) error {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return fmt.Errorf("not a JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(z.pub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("signature: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
	}
	var claims struct {
		Iss, Sub string
	}
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if json.Unmarshal(h, &header) != nil || json.Unmarshal(c, &claims) != nil {
		return fmt.Errorf("header or claims are not JSON")
	}
	if header.Kid != "key-1" || claims.Iss != "user-admin-app" || claims.Sub != "user-admin-app" {
		return fmt.Errorf("kid %q, iss %q, sub %q", header.Kid, claims.Iss, claims.Sub)
	}
	return nil
}

func parseForm(body string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range strings.Split(body, "&") {
		k, v, _ := strings.Cut(kv, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, err
		}
		val, err := url.QueryUnescape(v)
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func newTokenZitadel(t *testing.T, expires int) (*tokenZitadel, *Client, *KeyTokens) {
	t.Helper()
	key, priv := testKey(t)
	z := &tokenZitadel{t: t, pub: &priv.PublicKey, expires: expires, accepted: map[string]bool{},
		secrets: []string{strings.TrimSpace(strings.Split(key.Key, "\n")[1])}}
	srv := httptest.NewServer(z)
	t.Cleanup(srv.Close)
	tokens, err := NewKeyTokens(context.Background(), srv.URL, key, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return z, NewClient(srv.URL, tokens, srv.Client()), tokens
}

// Verifies: admin-zitadel AC-1; T-02
func TestClientUsesJWTProfileToken(t *testing.T) {
	z, c, _ := newTokenZitadel(t, 3600)
	account, err := c.ServiceAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.UserID != "user-admin-app" {
		t.Errorf("service account = %+v", account)
	}
	if z.issued != 1 || z.apiCalls != 1 {
		t.Errorf("tokens issued %d, API calls %d; want 1 and 1", z.issued, z.apiCalls)
	}
}

// Verifies: admin-zitadel AC-2
func TestTokenRenewedBeforeExpiry(t *testing.T) {
	z, c, tokens := newTokenZitadel(t, 3600)
	now := time.Now()
	tokens.now = func() time.Time { return now }
	call := func() {
		t.Helper()
		c.account = nil // ServiceAccount keeps its answer; ask again
		if _, err := c.ServiceAccount(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	call()
	call()
	if z.issued != 1 {
		t.Fatalf("tokens issued after two calls = %d, want 1 (reused)", z.issued)
	}
	// Shortly before the expiry the next call gets a new token first; the
	// old one is no longer accepted, as if it had expired.
	now = now.Add(time.Hour - renewBefore + time.Second)
	z.mu.Lock()
	z.accepted["access-1"] = false
	z.mu.Unlock()
	call()
	if z.issued != 2 {
		t.Fatalf("tokens issued near expiry = %d, want 2", z.issued)
	}
	if z.apiCalls != 3 {
		t.Errorf("API calls = %d, want 3 (no call refused for an expired token)", z.apiCalls)
	}
}

// A short-lived token (the prototype's live 60 s) is reused too, and renewed
// when a quarter of its life is left.
//
// Verifies: admin-zitadel AC-2
func TestShortTokenRenewedBeforeExpiry(t *testing.T) {
	z, c, tokens := newTokenZitadel(t, 60)
	now := time.Now()
	tokens.now = func() time.Time { return now }
	call := func() {
		t.Helper()
		c.account = nil
		if _, err := c.ServiceAccount(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	call()
	now = now.Add(40 * time.Second)
	call()
	if z.issued != 1 {
		t.Fatalf("tokens issued within 40 s = %d, want 1", z.issued)
	}
	now = now.Add(6 * time.Second) // 14 s left, less than a quarter
	call()
	if z.issued != 2 {
		t.Fatalf("tokens issued with 14 of 60 s left = %d, want 2", z.issued)
	}
}

// Verifies: admin-zitadel AC-3
func TestUnauthenticatedRetriesOnceWithNewToken(t *testing.T) {
	t.Run("a revoked token is replaced", func(t *testing.T) {
		z, c, _ := newTokenZitadel(t, 3600)
		if _, err := c.ServiceAccount(context.Background()); err != nil {
			t.Fatal(err)
		}
		z.mu.Lock()
		z.accepted["access-1"] = false // revoked in Zitadel
		z.mu.Unlock()
		c.account = nil
		if _, err := c.ServiceAccount(context.Background()); err != nil {
			t.Fatalf("call after revocation: %v", err)
		}
		if z.issued != 2 || z.apiCalls != 3 {
			t.Errorf("tokens issued %d, API calls %d; want 2 and 3", z.issued, z.apiCalls)
		}
	})
	t.Run("a second 401 goes to the caller", func(t *testing.T) {
		z, c, _ := newTokenZitadel(t, 3600)
		z.mu.Lock()
		z.refuseAll = true
		z.mu.Unlock()
		_, err := c.ServiceAccount(context.Background())
		if !HasCode(err, CodeUnauthenticated) {
			t.Fatalf("err = %v, want unauthenticated", err)
		}
		if z.apiCalls != 2 {
			t.Errorf("API calls = %d, want 2 (one retry)", z.apiCalls)
		}
	})
	t.Run("a request body is sent again", func(t *testing.T) {
		z, c, _ := newTokenZitadel(t, 3600)
		if _, err := c.ServiceAccount(context.Background()); err != nil {
			t.Fatal(err)
		}
		z.mu.Lock()
		z.accepted["access-1"] = false
		z.mu.Unlock()
		var answer map[string]any
		if err := c.call(context.Background(), userService, "GetUserByID", map[string]string{"userId": "7"}, &answer); err != nil {
			t.Fatalf("POST after revocation: %v", err)
		}
		if n := len(z.apiBodies); n != 3 || z.apiBodies[1] != `{"userId":"7"}` || z.apiBodies[2] != z.apiBodies[1] {
			t.Errorf("API bodies = %q, want the refused body sent again unchanged", z.apiBodies)
		}
	})
}

// A broken answer of the token endpoint is an error for the caller, and no
// API call is made with it.
//
// Verifies: admin-zitadel T-02
func TestBrokenTokenAnswerIsAnError(t *testing.T) {
	for name, answer := range map[string]struct {
		status int
		body   string
	}{
		"not JSON":           {http.StatusOK, `{"access_token":`},
		"no access token":    {http.StatusOK, `{"token_type":"Bearer","expires_in":60}`},
		"an HTML error page": {http.StatusBadGateway, `<html>bad gateway</html>`},
		"an empty body":      {http.StatusOK, ``},
	} {
		t.Run(name, func(t *testing.T) {
			apiCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/oauth/v2/token" {
					apiCalls++
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(answer.status)
				_, _ = w.Write([]byte(answer.body))
			}))
			t.Cleanup(srv.Close)
			key, _ := testKey(t)
			tokens, err := NewKeyTokens(context.Background(), srv.URL, key, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if tok, err := tokens.Token(context.Background()); err == nil {
				t.Fatalf("token = %q, want an error", tok)
			}
			c := NewClient(srv.URL, tokens, srv.Client())
			if _, err := c.ServiceAccount(context.Background()); err == nil {
				t.Error("ServiceAccount succeeded without a token")
			}
			if apiCalls != 0 {
				t.Errorf("API calls = %d, want none without a token", apiCalls)
			}
		})
	}
}
