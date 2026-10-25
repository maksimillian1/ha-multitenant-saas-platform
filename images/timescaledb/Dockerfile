# TimescaleDB extension image for the usage cluster (ADR-0008, HAMSP-25).
#
# CNPG mounts this image read-only into the PostgreSQL pods (ImageVolume,
# spec.postgresql.extensions) and points dynamic_library_path at lib/ and
# extension_control_path at share/. It holds only the extension's files; the
# PostgreSQL image stays the official CNPG one.
#
# These are the Timescale License (TSL) builds from Timescale's Debian
# repository: compression, continuous aggregates and policies are TSL features.
# The TSL allows using them inside our own service, not offering TimescaleDB as
# a database service. See licenses/ in the image.
#
# The Debian release and PostgreSQL major must match the cluster's image
# (ghcr.io/cloudnative-pg/postgresql:18.x-system-trixie).

ARG DEBIAN_RELEASE=trixie

FROM debian:${DEBIAN_RELEASE}-slim AS fetch
ARG DEBIAN_RELEASE
ARG PG_MAJOR=18
ARG TIMESCALEDB_VERSION=2.27.2
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gpg \
 && curl -fsSL https://packagecloud.io/timescale/timescaledb/gpgkey | gpg --dearmor -o /usr/share/keyrings/timescaledb.gpg \
 && echo "deb [signed-by=/usr/share/keyrings/timescaledb.gpg] https://packagecloud.io/timescale/timescaledb/debian/ ${DEBIAN_RELEASE} main" \
      > /etc/apt/sources.list.d/timescaledb.list \
 && apt-get update
# Download only: the packages depend on PGDG's postgresql-18, which the CNPG
# image already provides. The loader and the versioned library come in
# separate packages; both are pinned to the same release.
WORKDIR /debs
RUN apt-get download \
      "timescaledb-2-loader-postgresql-${PG_MAJOR}=${TIMESCALEDB_VERSION}~*" \
      "timescaledb-2-${TIMESCALEDB_VERSION}-postgresql-${PG_MAJOR}" \
 && for deb in *.deb; do dpkg-deb -x "$deb" /pkg; done \
 && mkdir -p /ext/lib /ext/share/extension /ext/licenses \
 && cp /pkg/usr/lib/postgresql/${PG_MAJOR}/lib/timescaledb*.so /ext/lib/ \
 && cp /pkg/usr/share/postgresql/${PG_MAJOR}/extension/timescaledb* /ext/share/extension/ \
 && cp -r /pkg/usr/share/doc/. /ext/licenses/ \
 && curl -fsSL -o /ext/licenses/LICENSE-APACHE \
      "https://raw.githubusercontent.com/timescale/timescaledb/${TIMESCALEDB_VERSION}/LICENSE-APACHE" \
 && curl -fsSL -o /ext/licenses/LICENSE-TIMESCALE \
      "https://raw.githubusercontent.com/timescale/timescaledb/${TIMESCALEDB_VERSION}/tsl/LICENSE-TIMESCALE" \
 && test -f "/ext/lib/timescaledb-tsl-${TIMESCALEDB_VERSION}.so" \
 && ls -R /ext

FROM scratch
COPY --from=fetch /ext/ /
