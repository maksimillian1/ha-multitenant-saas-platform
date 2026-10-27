package core

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// TenantHeader carries the tenant. The API Gateway sets it from the validated
// JWT and never takes it from the client (docs/contracts/gateway-headers.md).
const TenantHeader = "x-tenant-id"

var TenantRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ErrNotFound is returned when the item does not exist or belongs to another tenant.
var ErrNotFound = errors.New("item not found")

type Item struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// ItemStore keeps items per tenant. Every call sees only tenantID's items.
type ItemStore interface {
	Create(ctx context.Context, tenantID, name string) (Item, error)
	List(ctx context.Context, tenantID string) ([]Item, error)
	Get(ctx context.Context, tenantID, id string) (Item, error)
	Update(ctx context.Context, tenantID, id, name string) (Item, error)
	Delete(ctx context.Context, tenantID, id string) error
}
