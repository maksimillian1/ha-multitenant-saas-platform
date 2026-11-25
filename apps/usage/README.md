# Usage pipeline

One Go module, three binaries, one image each. Built from one `Dockerfile` by target; `docker buildx bake --load` builds all three.

| Binary | Image | Kind | Does | Database | Kafka |
|---|---|---|---|---|---|
| `cmd/writeapi` | `hamsp/usage-writeapi` | Deployment, wave 2 | UsageWriteAPI: HTTP → Kafka | none | produce |
| `cmd/writer` | `hamsp/usage-writer` | Deployment, wave 1 | Usage Writer: Kafka → TimescaleDB | `usage_writer` | consume |
| `cmd/migrate` | `hamsp/usage-migrate` | Job (Sync hook), wave 0 | applies `internal/migrations` | `usage_owner` | none |

Independent releases are a non-goal: the three images share the git SHA and roll out together. The split isolates code, image and credentials. Revisit when the writer's rebalance on every rollout shows as consumer lag; then use reproducible builds and pin digests, so unchanged images do not roll.

## Kafka access

Each binary that uses Kafka is its own KafkaUser (HAMSP-31, [kafka-client-auth](../../docs/features/kafka-client-auth.yaml)): it connects to the TLS listener `usage-kafka-kafka-bootstrap.kafka.svc:9093`, trusts only the cluster CA and logs in with SCRAM-SHA-512. ACLs give `usage-writeapi` produce on `usage-events`, `usage-writer` read on `usage-events` with its group `usage-writer` and produce on `usage-events-dlq`, and `usage-replay` read on `usage-events-dlq` and produce on `usage-events`; migrate has no Kafka login.

Records the writer cannot store go to `usage-events-dlq` (HAMSP-30, [contract](../../docs/contracts/usage-event/README.md#dead-letter-topic)); `usage_writer_records_dead_lettered_total` on `/metrics` counts them and `prototype/monitoring/usage-writer.rules.yaml` alerts on it. `writer replay -partition P -from A -to B` puts them back on `usage-events` after the cause is fixed; it runs as the Job in `prototype/usage-replay-job.yaml`, as `usage-replay`, within the DLQ's 7-day retention. The network policy on the brokers stays as a second layer.

The clients read `KAFKA_BROKERS`, `KAFKA_CA_FILE`, `KAFKA_USERNAME` and `KAFKA_PASSWORD` (`internal/kafka/auth.go`). A login without a CA file is refused at start: SCRAM runs only over TLS. With none of them set, the client connects without TLS and login, which the tests against kfake use. The CA on kind comes from `prototype/kafka-ca.sh` (`prototype/README.md`, "Kafka cluster CA").

## Import boundaries

| Package | Must never link |
|---|---|
| `internal/core` | anything but the standard library and `google/uuid` |
| `internal/writeapi`, `internal/kafka`, `internal/health`, `internal/httpx`, `cmd/writeapi` | `pgx`, `store`, `writer`, `migrations`, `testpg` |
| `internal/store`, `internal/writer`, `cmd/writer` | `writeapi`, `migrations`, `testpg` (tests may) |
| `internal/store` | `franz-go`, `kafka` |
| `internal/migrations`, `cmd/migrate` | `franz-go`, `kafka`, `writeapi`, `writer`, `store` |

An imported package runs its `init()` and stays in the binary even if nothing calls it, so the import graph is the boundary. Three checks enforce it in CI (`.github/workflows/usage.yml`):

- `golangci-lint run` (depguard, `.golangci.yml`): direct imports per file.
- `scripts/check-linked-deps.sh`: everything each binary links, through any chain.
- `go version -m` on the binaries in the images: no `pgx` in writeapi, no `franz-go` in migrate.

`cmd/*/main.go` only reads the configuration, wires the parts and runs them.

## Contract

The HTTP and Kafka contracts are the schemas in [docs/contracts/usage-event](../../docs/contracts/usage-event/README.md) (ADR-0010). `participants.json` there declares which versions writeapi and writer read and write; the tests in `internal/writeapi`, `internal/kafka` and `internal/writer` run against the examples of every declared version (`internal/contracttest`).

## Local checks

```bash
go vet ./... && go test ./...
scripts/check-linked-deps.sh
golangci-lint run ./...
```

Database tests need `TEST_DATABASE_URL` (`internal/testpg`); without it they are skipped.
