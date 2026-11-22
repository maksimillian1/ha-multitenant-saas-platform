# Usage events

How senders report usage to UsageWriteAPI (`apps/usage/cmd/writeapi`), and how UsageWriteAPI hands it to Usage Writer through Kafka. Introduced by HAMSP-26; the contract files and their CI checks by [ADR-0010](../../adr/0010-usage-event-contracts.md). Storage and deduplication are in [ADR-0006](../../adr/0006-usage-store.md) and [ADR-0007](../../adr/0007-usage-event-deduplication.md).

The formats are the files below. This page holds only what they cannot say; when the two disagree, the files win.

| File | Describes |
|---|---|
| [`openapi.yaml`](openapi.yaml) | `POST /vN/events` on UsageWriteAPI: body, answers 202, 400, 413, 503 and when to retry |
| [`schemas/event.v1.json`](schemas/event.v1.json) | One event: what happened. Shared by both channels |
| [`schemas/http-request.v1.json`](schemas/http-request.v1.json) | The HTTP body: a batch of 1 to 1 000 events |
| [`schemas/kafka-record.v1.json`](schemas/kafka-record.v1.json) | The value of a record in topic `usage-events`: one event and `schema_version` |
| [`participants.json`](participants.json) | Who produces and consumes which version on each channel; retention; when a version stopped |

## Senders

| Sender | Metric | Status |
|---|---|---|
| API Gateway | `api_calls` | HAMSP-26 |
| Agent workloads | agent tokens | later (HAMSP-18) |
| Egress Exporter | egress bytes | later (HAMSP-19) |

UsageWriteAPI trusts `tenant_id` in the event only because a `CiliumNetworkPolicy` lets nothing but the API Gateway's pods reach it, the same trust model as [`x-tenant-id`](../gateway-headers.md). A new sender needs its own way to prove its tenant (for agents: pod identity) before it is admitted.

## Rules the schemas cannot express

UsageWriteAPI checks these in `core.Validate`; every sender must keep them:

* `event_id` and `event_time` are set once by the sender and kept on every retry. Together with `tenant_id` they are the dedup key (ADR-0007): a retry with another time is a new event. Single events use UUIDv7; periodic totals derive `event_id` from tenant, metric and period.
* `event_time` is within [now − 7 days, now + 5 min].
* `attributes` is at most 4 KB. It never carries headers, query strings or bodies.
* A request is at most 1 MB.
* A 202 means Kafka confirmed every event (`acks=all`); the events are stored once however often they are sent. A 400 rejects the whole batch: do not retry it unchanged. A 503 is retried with the same `event_id` and `event_time`.

## Kafka

Topic `usage-events`. Key: `tenant_id`, so one tenant's events stay in order on one partition. Value: `kafka-record.vN.json`, written by UsageWriteAPI. Only UsageWriteAPI and writer replay produce, only Usage Writer consumes: each logs in as its own KafkaUser and its ACLs allow nothing else (HAMSP-31).

Usage Writer reads every version it declares in `participants.json`. A record without `schema_version` was written before the field existed and is read as v1. A newer version goes to the DLQ like any record the writer cannot store, which is why a new version is added to the writer before UsageWriteAPI writes it.

### Dead-letter topic

Topic `usage-events-dlq` (HAMSP-30, feature `usage-writer`). Usage Writer writes to it every record it cannot store: one that does not decode, one that fails validation, or one the database rejects as bad data (SQLSTATE class 22 or 23). The key, the value and the original headers are the original bytes, unchanged. The writer adds these headers:

| Header | Value |
|---|---|
| `dlq-reason` | Why the record could not be stored, for people; not parsed. |
| `dlq-source` | `<topic>/<partition>/<offset>` it was read from. |
| `dlq-time` | When it was written to the DLQ, RFC 3339 UTC. |
| `dlq-original-timestamp` | The original record's Kafka timestamp, RFC 3339 UTC. |

The DLQ record's own Kafka timestamp is the time of the DLQ write, so the 7-day retention counts from there. `writer replay` puts chosen DLQ records back on `usage-events` with the value unchanged and the header `replayed-from: dlq`; the dedup key (`tenant_id`, `event_id`, `event_time`) makes a second replay a no-op. Only Usage Writer writes the DLQ and only writer replay reads it.

## Examples and tests

Each schema carries `examples`. CI checks them against the schema; the services' tests run against the examples of every version they declare, so a declaration the code does not keep fails:

| Module | Test | Checks |
|---|---|---|
| `apps/gateway` | `usage/contract_test.go` | The body it sends has the fields of the example of each version it produces, of the same JSON kinds. |
| `apps/usage` writeapi | `internal/writeapi/contract_test.go` | Serves `/vN/events` for each version it consumes, accepts its examples, keeps attributes exact. |
| `apps/usage` writeapi | `internal/kafka/contract_test.go` | Writes exactly the Kafka version it declares, equal to that version's example. |
| `apps/usage` writer | `internal/writer/contract_test.go` | Reads the examples of each version it consumes; reads records without `schema_version` as v1; skips newer versions. |
| `apps/usage` writer | `internal/kafka/dlq_test.go` | A DLQ record keeps the original key, value and headers byte for byte and adds the DLQ headers. |

`response_bytes: 9007199254740993` (2^53 + 1) is there on purpose: a decoder that goes through `float64` changes it, and the tests compare it exactly.

## Changing the contract

* **Add an optional field**: no new version. Readers ignore fields they do not know.
* **Anything else** (rename, remove, retype, make required): a new version, in three pull requests, each deployed before the next:
  1. Expand: add `schemas/<name>.vN+1.json` with examples, and `vN+1` to every consumer's `consumes`. The consumers' code reads both.
  2. Switch: the producer writes `vN+1` and declares it; set `stopped.vN` to the merge date.
  3. Contract: drop `vN` from the consumers, no earlier than `stopped.vN` + `retention_days`.
* A merged `schemas/*.vN.json` is never edited. `openapi.yaml` may grow, but `oasdiff breaking` must pass.

`.github/contract-check/usage-event.sh` enforces all of this in CI. Locally (needs `jv`, `oasdiff`, `jq`):

```bash
.github/contract-check/usage-event.sh origin/master
```
