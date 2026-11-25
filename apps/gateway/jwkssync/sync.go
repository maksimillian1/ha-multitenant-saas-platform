package jwkssync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/auth"
)

const (
	KeepAfterRetired = 10 * time.Minute
	RewriteEvery     = time.Hour
)

type Store interface {
	Load(ctx context.Context) (auth.CacheFile, string, error)
	Save(ctx context.Context, c auth.CacheFile, version string) error
}

type Fetcher func(ctx context.Context) ([]jose.JSONWebKey, error)

func Sync(ctx context.Context, fetch Fetcher, store Store, now time.Time, log *slog.Logger) (bool, error) {
	current, err := fetch(ctx)
	if err != nil {
		return false, fmt.Errorf("jwks-sync: Zitadel: %w", err)
	}
	old, version, err := store.Load(ctx)
	if err != nil {
		return false, fmt.Errorf("jwks-sync: cache: %w", err)
	}
	next := merge(old, current, now)
	if sameKeys(old.Keys, next.Keys) && maps.Equal(old.Retired, next.Retired) && now.Sub(old.Updated) < RewriteEvery {
		return false, nil
	}
	if err := store.Save(ctx, next, version); err != nil {
		return false, fmt.Errorf("jwks-sync: cache: %w", err)
	}
	log.InfoContext(ctx, "JWKS cache written", "keys", keyIDs(next.Keys))
	return true, nil
}

// A key Zitadel stopped publishing is kept for one access token lifetime, so tokens it signed still verify.
func merge(old auth.CacheFile, current []jose.JSONWebKey, now time.Time) auth.CacheFile {
	next := auth.CacheFile{Updated: now, Keys: slices.Clone(current), Retired: map[string]time.Time{}}
	published := map[string]bool{}
	for _, k := range current {
		published[k.KeyID] = true
	}
	for _, k := range old.Keys {
		if published[k.KeyID] {
			continue
		}
		retired, ok := old.Retired[k.KeyID]
		if !ok {
			retired = now
		}
		if now.Sub(retired) <= KeepAfterRetired {
			next.Keys = append(next.Keys, k)
			next.Retired[k.KeyID] = retired
		}
	}
	next.Keys = sorted(next.Keys)
	return next
}

func sameKeys(a, b []jose.JSONWebKey) bool {
	if len(a) != len(b) {
		return false
	}
	ja, errA := json.Marshal(sorted(a))
	jb, errB := json.Marshal(sorted(b))
	return errA == nil && errB == nil && string(ja) == string(jb)
}

func sorted(keys []jose.JSONWebKey) []jose.JSONWebKey {
	out := slices.Clone(keys)
	slices.SortFunc(out, func(a, b jose.JSONWebKey) int {
		switch {
		case a.KeyID < b.KeyID:
			return -1
		case a.KeyID > b.KeyID:
			return 1
		}
		return 0
	})
	return out
}

func keyIDs(keys []jose.JSONWebKey) []string {
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.KeyID)
	}
	return ids
}
