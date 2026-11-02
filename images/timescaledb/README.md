# TimescaleDB extension image

The TimescaleDB extension of the usage cluster ([ADR-0008](../../docs/adr/0008-usage-store-on-timescaledb.md)), as a CloudNativePG ImageVolume extension: `FROM scratch`, only `lib/`, `share/extension/` and `licenses/`. CNPG mounts it into the official PostgreSQL 18 image.

```
ghcr.io/maksimillian1/ha-multitenant-saas-platform/timescaledb:<timescaledb>-<pg major>-<debian release>
```

e.g. `2.27.2-18-trixie`, for `linux/amd64` and `linux/arm64`. The official `timescaledb-oss` image of CNPG is the Apache 2 edition only, without the compression, aggregates and policies the usage database needs.

## Licence

The image holds the TSL builds from Timescale's Debian repository, unmodified, next to `licenses/LICENSE-TIMESCALE`. The Timescale License allows distributing them unmodified (section 2.1(c)) and forbids using them to offer TimescaleDB as a database service (section 2.2). Whoever uses the image is bound by it.

## Versions

`.github/workflows/timescaledb.yml` builds both architectures, runs `smoke-test.sh` on each and publishes the tag from the Dockerfile's `ARG`s on `master`, `HAMSP-23-platform-skeleton` or a manual run. A published tag is never overwritten, so every version stays available for a rollback. A pull request fails if `prototype/k8s/apps/usage-db/cluster.yaml` references another tag than the Dockerfile builds.

Each image carries two releases: `TIMESCALEDB_VERSION` with the loader, and `TIMESCALEDB_PREVIOUS_VERSION`. The loader loads the library of the version the database's extension is at, so the image works for a database at either release. The smoke test creates a database at the previous release and updates it.

## Upgrading and rolling back

TimescaleDB has no downgrade: once `ALTER EXTENSION timescaledb UPDATE` has run, only a restore from backup goes back. The upgrade is therefore two steps, each its own pull request:

1. **Image.** Set `TIMESCALEDB_VERSION` to the new release and `TIMESCALEDB_PREVIOUS_VERSION` to the one running now; reference the new tag in `cluster.yaml`. CNPG restarts the instances; the database stays at the old release and runs on its library from the new image. Rollback: reference the previous tag again.
2. **Extension.** Set `version` of the extension in the `Database` resource (`cluster.yaml`), which runs the update. From here, rolling back means restoring a backup taken before it.

`TIMESCALEDB_PREVIOUS_VERSION` must be the release the database is at. To skip releases, the image of step 1 still names that release as the previous one.
