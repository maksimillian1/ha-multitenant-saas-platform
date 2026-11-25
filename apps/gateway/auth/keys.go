package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	MaxCacheAge = 24 * time.Hour
	minRefetch  = 5 * time.Second
	retryEvery  = 15 * time.Second
	maxJWKS     = 1 << 20
)

var ErrNoKeys = errors.New("no signing keys from Zitadel or the cache")

type CacheFile struct {
	Updated  time.Time            `json:"updated"`
	Keys     []jose.JSONWebKey    `json:"keys"`
	LastSeen map[string]time.Time `json:"last_seen"`
}

func ParseJWKS(b []byte) ([]jose.JSONWebKey, error) {
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	if err := CheckKeys(set.Keys); err != nil {
		return nil, err
	}
	return set.Keys, nil
}

func CheckKeys(keys []jose.JSONWebKey) error {
	if len(keys) == 0 {
		return errors.New("jwks: no keys")
	}
	for _, k := range keys {
		if _, ok := k.Key.(*rsa.PublicKey); !ok {
			return fmt.Errorf("jwks: key %q is not an RSA public key", k.KeyID)
		}
		if k.KeyID == "" || (k.Algorithm != "" && k.Algorithm != string(jose.RS256)) || (k.Use != "" && k.Use != "sig") {
			return fmt.Errorf("jwks: key %q is not an RS256 signing key", k.KeyID)
		}
	}
	return nil
}

// Cached keys are used only until Zitadel answers, so a planted key lives no longer than the outage.
type KeySet struct {
	url       string
	client    *http.Client
	cacheFile string
	log       *slog.Logger
	now       func() time.Time

	mu          sync.RWMutex
	keys        []jose.JSONWebKey
	fromCache   bool
	lastAttempt time.Time
	fetchMu     sync.Mutex
}

func NewKeySet(url string, client *http.Client, cacheFile string, log *slog.Logger) *KeySet {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &KeySet{url: url, client: client, cacheFile: cacheFile, log: log, now: time.Now}
}

func (k *KeySet) Load(ctx context.Context) error {
	err := k.refetch(ctx, true)
	if err == nil {
		return nil
	}
	k.log.WarnContext(ctx, "JWKS from Zitadel failed", "err", err)
	cached, cerr := k.readCache()
	if cerr != nil {
		return errors.Join(ErrNoKeys, err, cerr)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.keys) == 0 {
		k.keys, k.fromCache = cached, true
		k.log.WarnContext(ctx, "using cached JWKS until Zitadel answers", "keys", len(cached))
	}
	return nil
}

func (k *KeySet) Run(ctx context.Context) {
	t := time.NewTicker(retryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		k.mu.RLock()
		stale := len(k.keys) == 0 || k.fromCache
		k.mu.RUnlock()
		if !stale {
			continue
		}
		if len(k.Keys()) == 0 {
			_ = k.Load(ctx)
		} else if err := k.refetch(ctx, true); err == nil {
			k.log.InfoContext(ctx, "JWKS from Zitadel replaces the cached keys")
		}
	}
}

func (k *KeySet) Ready() bool { return len(k.Keys()) > 0 }

func (k *KeySet) Keys() []jose.JSONWebKey {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.keys
}

func (k *KeySet) VerifySignature(ctx context.Context, jws *jose.JSONWebSignature) ([]byte, error) {
	if len(jws.Signatures) != 1 {
		return nil, errors.New("jws: want exactly one signature")
	}
	header := jws.Signatures[0].Header
	if header.Algorithm != string(jose.RS256) {
		return nil, fmt.Errorf("jws: algorithm %q, want RS256", header.Algorithm)
	}
	key, ok := k.find(header.KeyID)
	if !ok {
		if err := k.refetch(ctx, false); err != nil {
			return nil, fmt.Errorf("unknown key %q: %w", header.KeyID, err)
		}
		if key, ok = k.find(header.KeyID); !ok {
			return nil, fmt.Errorf("unknown key %q", header.KeyID)
		}
	}
	return jws.Verify(key.Key)
}

func (k *KeySet) find(kid string) (jose.JSONWebKey, bool) {
	for _, key := range k.Keys() {
		if key.KeyID == kid {
			return key, true
		}
	}
	return jose.JSONWebKey{}, false
}

func (k *KeySet) refetch(ctx context.Context, force bool) error {
	k.fetchMu.Lock()
	defer k.fetchMu.Unlock()
	k.mu.Lock()
	if !force && k.now().Sub(k.lastAttempt) < minRefetch {
		k.mu.Unlock()
		return errors.New("jwks: refetched moments ago")
	}
	k.lastAttempt = k.now()
	k.mu.Unlock()

	keys, err := FetchJWKS(ctx, k.client, k.url)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.keys, k.fromCache = keys, false
	k.mu.Unlock()
	return nil
}

func FetchJWKS(ctx context.Context, client *http.Client, url string) ([]jose.JSONWebKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKS))
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	return ParseJWKS(b)
}

func (k *KeySet) readCache() ([]jose.JSONWebKey, error) {
	if k.cacheFile == "" {
		return nil, errors.New("jwks cache: no file configured")
	}
	b, err := os.ReadFile(k.cacheFile)
	if err != nil {
		return nil, fmt.Errorf("jwks cache: %w", err)
	}
	var c CacheFile
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("jwks cache: %w", err)
	}
	if age := k.now().Sub(c.Updated); c.Updated.IsZero() || age > MaxCacheAge {
		return nil, fmt.Errorf("jwks cache: written %s ago, older than %s", age.Round(time.Minute), MaxCacheAge)
	}
	if err := CheckKeys(c.Keys); err != nil {
		return nil, fmt.Errorf("jwks cache: %w", err)
	}
	return c.Keys, nil
}
