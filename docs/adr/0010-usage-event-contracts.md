# ADR-0010: Usage event contracts are OpenAPI for HTTP and JSON Schema in Confluent Schema Registry for Kafka

Date: 2026-11-02

## Status

Accepted

Decided in HAMSP-25, when `apps/usage` was split into three binaries. Keeps [ADR-0005](0005-kafka-on-strimzi.md) (Kafka on Strimzi) and [ADR-0007](0007-usage-event-deduplication.md) (the dedup key).

## Context

A usage event crosses two contracts:

* **HTTP**: the API Gateway calls `POST /v1/events` on UsageWriteAPI. The sender is another Go module (`apps/gateway`) with its own event type.
* **Kafka**: UsageWriteAPI writes the event to topic `usage-events`; Usage Writer reads it. Records stay 7 days, so a writer reads records of every version written in that time.

Until now both contracts were a Markdown page ([usage-events.md](../contracts/usage-events.md)) and fixtures that the three code paths test against ([usage-event/](../contracts/usage-event/README.md)). A shared Go type was rejected: it does not protect the runtime contract, because the topic holds records of older versions, and it couples the modules at build time. What is missing is a machine-readable schema: something a new sender (agents, Egress Exporter) can read, CI can diff, and Kafka can enforce for producers outside this repository.

## Decision

### 1. HTTP: OpenAPI 3.1

[`openapi.yaml`](../contracts/usage-event/openapi.yaml) describes `POST /v1/events`: the request body, and the answers 202, 400, 413 and 503 with their bodies. The body schemas are JSON Schema 2020-12 files in [`schemas/`](../contracts/usage-event/schemas/), which OpenAPI 3.1 references directly. The version of the HTTP contract is the path: a breaking change is `/v2/events`, served next to `/v1` until every sender moved.

### 2. Kafka: JSON Schema, registered in Confluent Schema Registry

The record value stays JSON. Its schema is [`schemas/kafka-record.v1.json`](../contracts/usage-event/schemas/kafka-record.v1.json): the event of `event.v1.json` plus `schema_version`. HTTP and Kafka share one event schema.

The schema is registered in Confluent Schema Registry as subject `usage-events-value`, compatibility `BACKWARD_TRANSITIVE`: a new version must read every earlier one, because the topic may still hold any of them. Schemas are registered by CI or a Job before the rollout, never by a producer (`auto.register.schemas=false`); the registry refuses an incompatible version, so the change stops before it is deployed. Producers write the Confluent wire format (magic byte, schema ID, JSON); consumers fetch a schema once per ID and cache it, so the registry is not called per record.

The registry is not deployed yet. Until it is, the Kafka contract is enforced by `schema_version` in the record, the fixtures and the CI checks of item 4. `schema_version` stays after the switch until no record without a schema ID is left in the topic (7 days).

### 3. The schemas are the source of truth; every service validates on its own

Every sender and receiver implements its validation from the schemas in `docs/contracts/usage-event`: there is no shared Go package and no generated code yet. Two rules cannot be written in JSON Schema and stay in UsageWriteAPI's `core.Validate`: `event_time` within [now − 7 days, now + 5 min], and `attributes` at most 4 KB. Code generation from the schemas waits for a third consumer.

### 4. CI enforces the schemas

* [`.github/workflows/contracts.yml`](../../.github/workflows/contracts.yml) runs [`usage-event.sh`](../../.github/contract-check/usage-event.sh):
  * every valid fixture passes its schema; every invalid, unsupported and legacy fixture fails it (`jv`, format assertions on);
  * `oasdiff breaking` against the base branch fails on a breaking change of the HTTP contract, including changes inside referenced schemas;
  * a versioned schema or fixture that exists on the base branch may not be changed or deleted; a change adds a version.
* The Go tests of gateway, writeapi and writer run against the same fixtures (`gateway.yml`, `usage.yml`), so the code is checked against the examples that the schemas are checked against.

### 5. Security guardrail: the registry is platform-only

Confluent Schema Registry is under the Confluent Community License: free to run and change, but it may not be offered as an online service that competes with Confluent. As with TimescaleDB in [ADR-0008](0008-usage-store-on-timescaledb.md) §5, the platform uses it internally only: tenants never register schemas and never reach the registry. Only the registration Job writes to it; UsageWriteAPI and Usage Writer only read (part of HAMSP-31).

## Considered options

| Option | Verdict |
|---|---|
| Fixtures and Markdown only (status quo) | Rejected. Nothing machine-readable for new senders, and nothing refuses a breaking change outside this repository. Kept as the examples the schemas are tested against. |
| A shared Go type | Rejected. Couples modules at build time and says nothing about records of older versions in the topic. |
| Apicurio Registry (Apache 2.0) | Rejected. A registry for every artifact type, with an operator and a UI: more to run than one subject needs. Strong for large estates with many teams and APIs. Note: its memory footprint is in the same range as Confluent's (both JVM services); the difference is the operational surface, not the heap. |
| Karapace (Apache 2.0, Confluent API) | Rejected for lower adoption; it stays the drop-in replacement if the Confluent license ever becomes a problem, since clients do not change. |
| AsyncAPI for Kafka | Not now. Documents channels; enforces nothing. Can be added on top of the same JSON Schemas. |
| Avro or Protobuf instead of JSON | Rejected. Every sender and both binaries speak JSON today; JSON Schema is supported by the registry. |
| TypeSpec or Smithy to author the HTTP API | Rejected. One endpoint; plain OpenAPI is enough. |

## Consequences

* New senders read `openapi.yaml` and the schemas, not Go code.
* A breaking change of either contract fails CI; for Kafka it will also be refused by the registry once it runs.
* The registry is one more stateful JVM service, on the start path of UsageWriteAPI and of a writer meeting a new schema ID: when it is down, running pods keep working on their cache, new pods do not start. It needs two replicas, its storage topic `_schemas` in the usage Kafka cluster, and monitoring.
* Rolling out the registry is separate work: deploying it, the registration Job before the migrate Job, the wire format in `internal/kafka`, and reading records without a schema ID during the switch.
* The Confluent Community License applies (item 5).
