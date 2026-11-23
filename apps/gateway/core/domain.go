package core

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// docs/contracts/gateway-headers.md
const TenantHeader = "x-tenant-id"

type UsageEvent struct {
	ID         string         `json:"event_id"`
	Time       time.Time      `json:"event_time"`
	TenantID   string         `json:"tenant_id"`
	Metric     string         `json:"metric"`
	Quantity   int64          `json:"quantity"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

func APICallEvent(tenant string, start time.Time, method, path string, status int, duration time.Duration, reqBytes, respBytes int64) UsageEvent {
	return UsageEvent{
		ID: uuid.Must(uuid.NewV7()).String(), Time: start.UTC(), TenantID: tenant, Metric: "api_calls", Quantity: 1,
		Attributes: map[string]any{
			"method": method, "path": path, "status": status, "duration_ms": duration.Milliseconds(),
			"request_bytes": reqBytes, "response_bytes": respBytes,
		},
	}
}

type Authenticator interface {
	Tenant(ctx context.Context, token string) (string, error)
}

// Report must not block.
type UsageReporter interface {
	Report(UsageEvent)
}
