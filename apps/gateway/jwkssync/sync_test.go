package jwkssync

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/auth"
)

func newKey(t *testing.T, kid string) jose.JSONWebKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return jose.JSONWebKey{Key: k, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"}
}

type memStore struct {
	cache  auth.CacheFile
	writes int
}

func (m *memStore) Load(context.Context) (auth.CacheFile, string, error) { return m.cache, "1", nil }

func (m *memStore) Save(_ context.Context, c auth.CacheFile, _ string) error {
	m.cache = c
	m.writes++
	return nil
}

func zitadelServing(t *testing.T, body *atomic.Value) Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := body.Load().([]byte)
		if b == nil {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return func(ctx context.Context) ([]jose.JSONWebKey, error) {
		return auth.FetchJWKS(ctx, srv.Client(), srv.URL)
	}
}

func jwks(t *testing.T, keys ...jose.JSONWebKey) []byte {
	t.Helper()
	b, err := json.Marshal(jose.JSONWebKeySet{Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ids(c auth.CacheFile) []string { return keyIDs(c.Keys) }

// Verifies: gateway-auth AC-7
func TestSyncMergesAndRejectsBadSets(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	k1, k2 := newKey(t, "k1"), newKey(t, "k2")
	var body atomic.Value
	fetch := zitadelServing(t, &body)
	store := &memStore{}
	start := time.Now()

	step := func(name string, at time.Duration, served []byte, wantWrite bool, wantIDs ...string) {
		t.Helper()
		body.Store(served)
		before := store.writes
		_, err := Sync(ctx, fetch, store, start.Add(at), log)
		if wantWrite && err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if wrote := store.writes > before; wrote != wantWrite {
			t.Errorf("%s: wrote %v, want %v", name, wrote, wantWrite)
		}
		if got := ids(store.cache); !slices.Equal(got, wantIDs) {
			t.Errorf("%s: cache has %v, want %v", name, got, wantIDs)
		}
	}

	step("first run", 0, jwks(t, k1.Public()), true, "k1")
	step("nothing changed", time.Minute, jwks(t, k1.Public()), false, "k1")
	step("Zitadel rotates to k2", 2*time.Minute, jwks(t, k2.Public()), true, "k1", "k2")
	step("k1 within one token lifetime", 11*time.Minute, jwks(t, k2.Public()), false, "k1", "k2")
	step("k1 past one token lifetime", 13*time.Minute, jwks(t, k2.Public()), true, "k2")
	step("Zitadel down", 14*time.Minute, nil, false, "k2")
	step("a private key", 15*time.Minute, jwks(t, k1), false, "k2")
	step("not a JWKS", 16*time.Minute, []byte("<html>"), false, "k2")
	step("an hour without change", 13*time.Minute+RewriteEvery, jwks(t, k2.Public()), true, "k2")
	if !store.cache.Updated.Equal(start.Add(13*time.Minute + RewriteEvery)) {
		t.Errorf("cache updated %s, want the last rewrite", store.cache.Updated)
	}
	for _, k := range store.cache.Keys {
		if !k.IsPublic() {
			t.Errorf("the cache holds a private key %q", k.KeyID)
		}
	}
}

func TestConfigMapKeepsResourceVersion(t *testing.T) {
	ctx := context.Background()
	k1 := newKey(t, "k1")
	cached, _ := json.Marshal(auth.CacheFile{Updated: time.Now(), Keys: []jose.JSONWebKey{k1.Public()}})
	var put configMap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/gateway/configmaps/zitadel-jwks" || r.Header.Get("Authorization") != "Bearer sa-token" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&put)
			if put.Metadata["resourceVersion"] != "41" {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(configMap{APIVersion: "v1", Kind: "ConfigMap",
			Metadata: map[string]any{"name": "zitadel-jwks", "resourceVersion": "41"},
			Data:     map[string]string{DataKey: string(cached)}})
	}))
	t.Cleanup(srv.Close)
	cm := NewConfigMap(srv.URL, "gateway", "zitadel-jwks", func() (string, error) { return "sa-token", nil }, srv.Client())

	c, version, err := cm.Load(ctx)
	if err != nil || version != "41" || !slices.Equal(ids(c), []string{"k1"}) {
		t.Fatalf("Load = %v, %q, %v; want k1 at version 41", ids(c), version, err)
	}
	if err := cm.Save(ctx, c, version); err != nil {
		t.Fatal(err)
	}
	if put.Data[DataKey] == "" {
		t.Error("Save sent no jwks.json")
	}
	if err := cm.Save(ctx, c, "40"); err == nil {
		t.Error("a write with a stale resourceVersion succeeded")
	}
}
