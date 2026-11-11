package lifecycle

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/internal/fakes"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/store/storetest"
)

// newService wires the lifecycle to a fresh database and the fakes.
func newService(t *testing.T) (*Service, *fakes.Deps) {
	t.Helper()
	db := storetest.New(t)
	deps := fakes.New()
	ids := []string{
		"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444",
	}
	var mu sync.Mutex
	next := 0
	s := NewService(db.Open(t), deps, deps, slog.New(slog.NewTextHandler(io.Discard, nil)), func() string {
		mu.Lock()
		defer mu.Unlock()
		id := ids[next]
		next++
		return id
	})
	s.StepTimeout = 200 * time.Millisecond
	s.DeleteTimeout = 200 * time.Millisecond
	return s, deps
}
