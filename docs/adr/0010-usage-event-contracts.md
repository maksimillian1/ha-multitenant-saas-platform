# ADR-0010: Usage event contracts are JSON Schema and OpenAPI files, enforced by CI

Date: 2026-11-02

## Status

Accepted

Decided in HAMSP-25, when `apps/usage` was split into three binaries. Keeps [ADR-0005](0005-kafka-on-strimzi.md) (Kafka on Strimzi) and [ADR-0007](0007-usage-event-deduplication.md) (the dedup key).

## Context

A usage event crosses two contracts:

* **HTTP**: the API Gateway calls `POST /v1/events` on UsageWriteAPI. The sender is another Go module (`apps/gateway`) with its own event type.
* **Kafka**: UsageWriteAPI writes the event to topic `usage-events`; Usage Writer reads it. Records stay 7 days, so a writer reads records of every version written in that time.

The three participants deploy separately: gateway and usage are different Argo CD applications, and inside usage writer rolls out before writeapi. A contract change therefore cannot be atomic. For a while, one side runs the new version and the other the old one.

Until now both contracts were a Markdown page and fixture files. A shared Go type was rejected: it does not describe records of older versions still in the topic, and it couples the modules at build time. What was missing: a machine-readable schema for new senders (agents, Egress Exporter), a check that refuses a breaking change, and a way to migrate step by step without a deploy that changes everything at once.

## Decision

### 1. The contract is files

[`docs/contracts/usage-event/`](../contracts/usage-event/README.md) holds:

* `openapi.yaml`: OpenAPI 3.1 for `POST /vN/events`, with the answers 202, 400, 413 and 503;
* `schemas/*.vN.json`: JSON Schema 2020-12. `event.v1.json` is the event, shared by `http-request.v1.json` (the request body) and `kafka-record.v1.json` (the record value);
* the examples inside each schema (`examples`), which the services' tests run against;
* `participants.json`: which service produces and consumes which version on each channel, the channel's retention, and when each version stopped being produced.

The files are the source of truth. Every service implements its own validation from them; there is no shared Go package and no generated code. Two rules cannot be written in JSON Schema and stay in UsageWriteAPI's `core.Validate`: `event_time` within [now − 7 days, now + 5 min], and `attributes` at most 4 KB. Code generation waits for a third consumer.

### 2. Changes: tolerant reader first, expand and contract when that is not enough

* **Additive change, no new version.** A new optional field is added to the current schema's successor only when it is needed; readers ignore fields they do not know (the schemas do not close `additionalProperties`, and the Go decoders ignore unknown fields). Most changes end here.
* **Breaking change, new version.** Renaming, removing or retyping a field adds `vN+1` and goes through three pull requests:
  1. **Expand**: every consumer reads `vN` and `vN+1` and declares both.
  2. **Switch**: the producer writes `vN+1`; `stopped.vN` gets the merge date.
  3. **Contract**: consumers drop `vN`, at the earliest after the channel's retention (7 days for Kafka, 0 for HTTP) has passed since `stopped.vN`.

  For HTTP, UsageWriteAPI serves `/vN` and `/vN+1` side by side between steps 1 and 3.

### 3. CI enforces it

[`.github/workflows/contracts.yml`](../../.github/workflows/contracts.yml) runs [`usage-event.sh`](../../.github/contract-check/usage-event.sh) (`jv`, `oasdiff` and `jq`, installed in CI only):

| Rule | Fails when |
|---|---|
| Schemas and examples | a schema does not compile, or one of its examples does not pass it |
| Declared versions exist | `participants.json` names a version without a schema with examples |
| Produce only what everyone reads | a producer writes a version that some consumer of the channel does not read |
| Gateway endpoint | the gateway's `USAGE_URL` calls a version the gateway does not declare |
| No breaking HTTP change | `oasdiff breaking` against the base branch reports one, also inside referenced schemas |
| Merged schemas are immutable | a `schemas/*.vN.json` that exists on the base branch is changed or deleted |
| Stop is recorded | a version leaves every producer without a `stopped` date |
| Contract after drain | a consumer drops a version that was still produced before the PR, or before `stopped` + retention |

The declarations are tied to the code by the Go tests: gateway, writeapi and writer run against the examples of every version they declare, so a declaration the code does not keep fails (`apps/usage/internal/contracttest`, `apps/gateway/usage/contract_test.go`). UsageWriteAPI also has to serve the route of every HTTP version it declares.

CI sees the repository, not the cluster. Steps 1 to 3 are separate pull requests so that each is deployed before the next is merged.

## Considered options

| Option | Verdict |
|---|---|
| Fixture files and Markdown (status quo) | Replaced. The examples moved into the schemas; nothing machine-readable existed for new senders. |
| A shared Go type | Rejected. Couples modules at build time; says nothing about records of older versions in the topic. |
| Lockstep: one version, everything changes in one PR | Rejected. Gateway and usage deploy separately, and Kafka keeps old records for 7 days. |
| Confluent Schema Registry (Confluent Community License) | Rejected for now. A stateful JVM service on the start path of the producers and consumers, plus a license that forbids offering it as a service. Its compatibility check is what rules 3 to 8 do in CI, as long as every producer and consumer of a channel lives in this repository. |
| Apicurio Registry, Karapace | Rejected for the same reason: one more stateful service. |
| Pact with a broker | Rejected: the broker is one more service. |
| Dual publishing to a topic per version | Rejected: two topics and double writes for every change. |
| AsyncAPI for Kafka | Not now. Documents channels and enforces nothing; can be added on top of the same schemas. |
| Avro or Protobuf instead of JSON | Rejected. Every participant speaks JSON. |

**Revisit when** a channel gets a producer or consumer outside this repository (Billing, Metering Query, a partner): CI then no longer sees every participant, and a schema registry becomes the place where compatibility is checked.

## Consequences

* New senders read `openapi.yaml` and the schemas, not Go code.
* A breaking change needs a new version and three pull requests; CI refuses shortcuts. Most changes are additive and need none.
* `participants.json` must be kept true. The Go tests keep it honest for the declared versions; a service that reads the channel without being listed is not caught.
* `stopped` dates are the merge dates of the switch, not deploy times. A deploy that lags more than the retention after merge needs the contract step held back by hand.
* No new runtime component, license or package in `go.mod`.
