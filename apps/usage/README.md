# Usage pipeline

One Go module, three binaries, one image each. Built from one `Dockerfile` by target; `docker buildx bake --load` builds all three.

| Binary | Image | Kind | Does | Database | Kafka |
|---|---|---|---|---|---|
| `cmd/writeapi` | `hamsp/usage-writeapi` | Deployment, wave 2 | UsageWriteAPI: HTTP → Kafka | none | produce |
| `cmd/writer` | `hamsp/usage-writer` | Deployment, wave 1 | Usage Writer: Kafka → TimescaleDB | `usage_writer` | consume |
| `cmd/migrate` | `hamsp/usage-migrate` | Job (Sync hook), wave 0 | applies `internal/migrations` | `usage_owner` | none |

Independent releases are a non-goal: the three images share the git SHA and roll out together. The split isolates code, image and credentials. Revisit when the writer's rebalance on every rollout shows as consumer lag; then use reproducible builds and pin digests, so unchanged images do not roll.

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
