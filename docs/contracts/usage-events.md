# Usage events → UsageWriteAPI

The contract between senders of usage events and UsageWriteAPI (`apps/usage`, command `writeapi`). Introduced by HAMSP-26. Storage and deduplication are in [ADR-0006](../adr/0006-usage-store.md) and [ADR-0007](../adr/0007-usage-event-deduplication.md).

## Senders

| Sender | Metric | Status |
|---|---|---|
| API Gateway | `api_calls` | HAMSP-26 |
| Agent workloads | agent tokens | later (HAMSP-18) |
| Egress Exporter | egress bytes | later (HAMSP-19) |

UsageWriteAPI trusts `tenant_id` in the event only because a `CiliumNetworkPolicy` lets nothing but the API Gateway's pods reach it, the same trust model as [`x-tenant-id`](gateway-headers.md). A new sender needs its own way to prove its tenant (for agents: pod identity) before it is admitted.

## `POST /v1/events`

```json
{
  "events": [
    {
      "event_id": "0192f6a8-7c1e-7b3a-9c4d-2f1e8a6b5c3d",
      "event_time": "2026-10-24T20:15:03.123Z",
      "tenant_id": "396438053259838069",
      "metric": "api_calls",
      "quantity": 1,
      "attributes": {"method": "GET", "path": "/items/42", "status": 200,
                     "duration_ms": 12, "request_bytes": 0, "response_bytes": 87}
    }
  ]
}
```

| Field | Rule |
|---|---|
| `event_id` | UUID, set once by the sender and kept on every retry. Single events use UUIDv7; periodic totals derive it from tenant, metric and period. |
| `event_time` | RFC 3339, set once by the sender and kept on every retry. Within [now − 7 days, now + 5 min]. Part of the dedup key, so a retry with another time is a new event. |
| `tenant_id` | Zitadel Organization ID, `[A-Za-z0-9_-]{1,64}`. |
| `metric` | Known metric: `api_calls`. |
| `quantity` | Integer > 0. |
| `attributes` | Optional JSON object, at most 4 KB. For `api_calls`: `method`, `path` (no query string), `status`, `duration_ms`, `request_bytes`, `response_bytes`. Never headers, query strings or bodies. |

At most 1 000 events and 1 MB per request.

| Answer | Meaning |
|---|---|
| `202 {"accepted": n}` | Kafka confirmed every event (`acks=all`). The events will be stored once, however often they are sent. |
| `400 {"error": "..."}` | The batch is invalid; none of its events was accepted. Do not retry it unchanged. |
| `503 {"error": "..."}` | Kafka did not confirm the write. Retry with the same `event_id` and `event_time`. |

## Kafka

Topic `usage-events`, key `tenant_id`, value: one event as JSON in the shape above. Only UsageWriteAPI produces and only Usage Writer consumes; other pods cannot reach the brokers.
