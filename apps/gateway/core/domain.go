package core

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// TenantHeader carries the caller's tenant to the services
// (docs/contracts/gateway-headers.md).
const TenantHeader = "x-tenant-id"

// UsageEvent is one usage event as UsageWriteAPI accepts it
// (docs/contracts/usage-events.md).
type UsageEvent struct {
	ID         string         `json:"event_id"`
	Time       time.Time      `json:"event_time"`
	TenantID   string         `json:"tenant_id"`
	Metric     string         `json:"metric"`
	Quantity   int64          `json:"quantity"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// APICallEvent is the usage event of one request served for a tenant.
func APICallEvent(tenant string, start time.Time, method, path string, status int, duration time.Duration, reqBytes, respBytes int64) UsageEvent {
	return UsageEvent{
		ID: uuid.Must(uuid.NewV7()).String(), Time: start.UTC(), TenantID: tenant, Metric: "api_calls", Quantity: 1,
		Attributes: map[string]any{
			"method": method, "path": path, "status": status, "duration_ms": duration.Milliseconds(),
			"request_bytes": reqBytes, "response_bytes": respBytes,
		},
	}
}

// Authenticator returns the tenant of a valid access token.
type Authenticator interface {
	Tenant(ctx context.Context, token string) (string, error)
}

// UsageReporter takes the usage event of every request served for a tenant
// (FR-05). Report must not block.
type UsageReporter interface {
	Report(UsageEvent)
}
