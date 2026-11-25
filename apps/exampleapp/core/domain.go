package core

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// The API Gateway sets it from the JWT, never from the client (docs/contracts/gateway-headers.md).
const TenantHeader = "x-tenant-id"

var TenantRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Also returned for another tenant's item, so IDs are not probed.
var ErrNotFound = errors.New("item not found")

var ErrQueryTimeout = errors.New("query timed out")

type Item struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type ItemStore interface {
	Create(ctx context.Context, tenantID, name string) (Item, error)
	List(ctx context.Context, tenantID string) ([]Item, error)
	Get(ctx context.Context, tenantID, id string) (Item, error)
	Update(ctx context.Context, tenantID, id, name string) (Item, error)
	Delete(ctx context.Context, tenantID, id string) error
}
