package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/health"
)

type fakeZitadel struct {
	srv  *httptest.Server
	down atomic.Bool
	keys atomic.Value
}

func newFakeZitadel(t *testing.T, keys ...jose.JSONWebKey) *fakeZitadel {
	t.Helper()
	z := &fakeZitadel{}
	z.serve(keys...)
	z.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if z.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: z.keys.Load().([]jose.JSONWebKey)})
	}))
	t.Cleanup(z.srv.Close)
	return z
}

func (z *fakeZitadel) serve(keys ...jose.JSONWebKey) {
	var public []jose.JSONWebKey
	for _, k := range keys {
		public = append(public, k.Public())
	}
	z.keys.Store(public)
}

func writeCache(t *testing.T, updated time.Time, keys ...jose.JSONWebKey) string {
	t.Helper()
	c := CacheFile{Updated: updated}
	for _, k := range keys {
		c.Keys = append(c.Keys, k.Public())
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwks.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readyStatus(keys *KeySet) int {
	rec := httptest.NewRecorder()
	health.NewService(keys.Ready).ReadyHandler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rec.Code
}

// Verifies: gateway-auth AC-5
func TestStartLoadsKeysOrStaysUnready(t *testing.T) {
	key := newKey(t, "k1")
	ctx := context.Background()
	cases := []struct {
		name      string
		down      bool
		cache     func(t *testing.T) string
		wantReady bool
	}{
		{"Zitadel answers", false, func(t *testing.T) string { return "" }, true},
		{"Zitadel down, fresh cache", true, func(t *testing.T) string { return writeCache(t, time.Now().Add(-23*time.Hour), key) }, true},
		{"Zitadel down, cache older than 24 h", true, func(t *testing.T) string { return writeCache(t, time.Now().Add(-25*time.Hour), key) }, false},
		{"Zitadel down, no cache file", true, func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.json") }, false},
		{"Zitadel down, no cache configured", true, func(t *testing.T) string { return "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			z := newFakeZitadel(t, key)
			z.down.Store(tc.down)
			keys := NewKeySet(z.srv.URL, z.srv.Client(), tc.cache(t), nil)
			err := keys.Load(ctx)
			if tc.wantReady != (err == nil) || keys.Ready() != tc.wantReady {
				t.Fatalf("Load err %v, Ready %v; want ready %v", err, keys.Ready(), tc.wantReady)
			}
			wantStatus := http.StatusOK
			if !tc.wantReady {
				wantStatus = http.StatusServiceUnavailable
			}
			if got := readyStatus(keys); got != wantStatus {
				t.Errorf("readiness %d, want %d", got, wantStatus)
			}
			v := NewVerifier(issuer, audience, zitadelAdminOrg, keys)
			_, err = v.Tenant(ctx, sign(t, key, claims(nil)))
			if tc.wantReady != (err == nil) {
				t.Errorf("token signed with k1: err %v, want accepted %v", err, tc.wantReady)
			}
		})
	}
}

// Verifies: gateway-auth AC-6; T-12
func TestZitadelAnswerReplacesCachedKeys(t *testing.T) {
	ctx := context.Background()
	planted, current := newKey(t, "planted"), newKey(t, "current")
	z := newFakeZitadel(t, current)
	z.down.Store(true)
	keys := NewKeySet(z.srv.URL, z.srv.Client(), writeCache(t, time.Now(), planted), nil)
	if err := keys.Load(ctx); err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(issuer, audience, zitadelAdminOrg, keys)
	if _, err := v.Tenant(ctx, sign(t, planted, claims(nil))); err != nil {
		t.Fatalf("during the outage the cached key is refused: %v", err)
	}

	z.down.Store(false)
	keys.lastAttempt = time.Time{}
	if _, err := v.Tenant(ctx, sign(t, current, claims(nil))); err != nil {
		t.Fatalf("a token with Zitadel's new key is refused: %v", err)
	}
	if _, err := v.Tenant(ctx, sign(t, planted, claims(nil))); err == nil {
		t.Error("a key only the cache had is still accepted after Zitadel answered")
	}
}

func TestUnknownKeyIDsDoNotFloodZitadel(t *testing.T) {
	ctx := context.Background()
	key := newKey(t, "k1")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key.Public()}})
	}))
	t.Cleanup(srv.Close)
	keys := NewKeySet(srv.URL, srv.Client(), "", nil)
	if err := keys.Load(ctx); err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(issuer, audience, zitadelAdminOrg, keys)
	for range 20 {
		_, _ = v.Tenant(ctx, sign(t, newKey(t, "made-up"), claims(nil)))
	}
	if n := calls.Load(); n > 2 {
		t.Errorf("Zitadel was asked %d times for 20 tokens with unknown key IDs", n)
	}
}

func TestParseJWKSRejectsUnsafeSets(t *testing.T) {
	key := newKey(t, "k1")
	pub, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key.Public()}})
	if _, err := ParseJWKS(pub); err != nil {
		t.Fatalf("a valid set is rejected: %v", err)
	}
	private, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key}})
	hmac, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "h", Algorithm: "HS256"}}})
	wrongAlg := key.Public()
	wrongAlg.Algorithm = "PS256"
	ps, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{wrongAlg}})
	for name, b := range map[string][]byte{
		"private key": private, "symmetric key": hmac, "other algorithm": ps,
		"empty": []byte(`{"keys":[]}`), "not json": []byte("<html>"),
	} {
		if _, err := ParseJWKS(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Verifies: gateway-auth T-11
func FuzzParseJWKS(f *testing.F) {
	key := newKey(&testing.T{}, "k1")
	seed, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key.Public()}})
	f.Add(seed)
	f.Add([]byte(`{"keys":[{"kty":"oct","k":"AA"}]}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		keys, err := ParseJWKS(b)
		if err != nil {
			return
		}
		for _, k := range keys {
			if !k.IsPublic() {
				t.Fatalf("accepted a private key %q", k.KeyID)
			}
		}
	})
}
