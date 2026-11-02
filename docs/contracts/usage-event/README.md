# Usage event contract

The contract of [usage-events.md](../usage-events.md) as schemas and examples ([ADR-0010](../../adr/0010-usage-event-contracts.md)):

| File | Describes |
|---|---|
| `openapi.yaml` | `POST /v1/events` on UsageWriteAPI (OpenAPI 3.1) |
| `schemas/event.v1.json` | One event (JSON Schema 2020-12), shared by HTTP and Kafka |
| `schemas/http-request.v1.json` | The request body: a batch of 1 to 1 000 events |
| `schemas/kafka-record.v1.json` | The value of a record in `usage-events`; subject `usage-events-value` in Schema Registry |

Every service validates on its own from these schemas. `core.Validate` adds the two rules JSON Schema cannot express: the `event_time` window and the 4 KB limit on `attributes`.

The fixtures are examples checked against the schemas in CI (`.github/contract-check/usage-event.sh`), and three modules test their code against the same files instead of sharing a Go type:

| Module | Test | Checks |
|---|---|---|
| `apps/gateway` | `usage/contract_test.go` | The body it sends has the fields of `http/v1/valid/api-calls.json`, of the same JSON kinds. |
| `apps/usage` writeapi | `internal/writeapi/contract_test.go` | Accepts every `http/v1/valid`, rejects every `http/v1/invalid`, keeps attributes byte for byte. |
| `apps/usage` writeapi | `internal/kafka/contract_test.go` | Encodes the HTTP fixture's first event as `kafka/v<SchemaVersion>/api-calls.json`. |
| `apps/usage` writer | `internal/kafka/contract_test.go`, `internal/writer/contract_test.go` | Decodes every `kafka/v*`; skips `kafka/unsupported` as malformed. |

There are two contracts with separate versions:

- `http/vN/`: `POST /vN/events`, sent by the API Gateway. The version is in the path.
- `kafka/vN/`: the Kafka record value, written by UsageWriteAPI with `schema_version: N`.
- `kafka/legacy/`: records from before `schema_version` existed. They do not match the current record schema, but Usage Writer reads them as version 1 while they can still be in the topic.
- `kafka/unsupported/`: a version newer than Usage Writer knows; it must be skipped, not stored.

Rules:

- A versioned schema or fixture is never edited once merged; CI fails on it. A change adds a version (`event.v2.json`, `kafka/v2/`, `/v2/events`).
- `openapi.yaml` may grow, but `oasdiff breaking` against the base branch must pass.
- A new Kafka version rolls out writer first, then writeapi: writer must read `vN` before anything writes it.
- `response_bytes: 9007199254740993` (2^53 + 1) is there on purpose: a decoder that goes through `float64` changes it, and the tests compare it exactly.
- Codegen from these files waits for a third consumer.
