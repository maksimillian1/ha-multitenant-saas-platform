# TimescaleDB extension image for the usage cluster (ADR-0008, HAMSP-25).
#
# CNPG mounts this image read-only into the PostgreSQL pods (ImageVolume,
# spec.postgresql.extensions) and points dynamic_library_path at lib/ and
# extension_control_path at share/. It holds only the extension's files; the
# PostgreSQL image stays the official CNPG one.
#
# These are the Timescale License (TSL) builds from Timescale's Debian
# repository, unmodified: compression, continuous aggregates and policies are
# TSL features. The TSL allows distributing them unmodified (section 2.1(c))
# and using them inside our own service, not offering TimescaleDB as a
# database service (section 2.2). See licenses/ in the image.
#
# The image also carries the previous release's libraries and SQL, so a
# database whose extension is still at that release keeps working after the
# image is replaced, until ALTER EXTENSION ... UPDATE (README.md).
#
# The Debian release and PostgreSQL major must match the cluster's image
# (ghcr.io/cloudnative-pg/postgresql:18.x-system-trixie). The published tag is
# <TIMESCALEDB_VERSION>-<PG_MAJOR>-<DEBIAN_RELEASE>, taken from these ARGs.

ARG DEBIAN_RELEASE=trixie

# Runs on the build machine for every target: apt only downloads and unpacks
# the target architecture's packages, so no emulation is needed (C-06).
FROM --platform=$BUILDPLATFORM debian:${DEBIAN_RELEASE}-slim AS fetch
ARG DEBIAN_RELEASE
ARG TARGETARCH
ARG PG_MAJOR=18
ARG TIMESCALEDB_VERSION=2.27.2
ARG TIMESCALEDB_PREVIOUS_VERSION=2.27.1
RUN dpkg --add-architecture "${TARGETARCH}" \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gpg \
 && curl -fsSL https://packagecloud.io/timescale/timescaledb/gpgkey | gpg --dearmor -o /usr/share/keyrings/timescaledb.gpg \
 && echo "deb [signed-by=/usr/share/keyrings/timescaledb.gpg] https://packagecloud.io/timescale/timescaledb/debian/ ${DEBIAN_RELEASE} main" \
      > /etc/apt/sources.list.d/timescaledb.list \
 && apt-get update
# Download only: the packages depend on PGDG's postgresql-18, which the CNPG
# image already provides. The loader (timescaledb.so and the control file)
# comes from the current release; each versioned package holds its release's
# libraries and SQL scripts, so the two releases do not overlap.
WORKDIR /debs
RUN apt-get download \
      "timescaledb-2-loader-postgresql-${PG_MAJOR}:${TARGETARCH}=${TIMESCALEDB_VERSION}~*" \
      "timescaledb-2-${TIMESCALEDB_VERSION}-postgresql-${PG_MAJOR}:${TARGETARCH}" \
      "timescaledb-2-${TIMESCALEDB_PREVIOUS_VERSION}-postgresql-${PG_MAJOR}:${TARGETARCH}" \
 && for deb in *.deb; do dpkg-deb -x "$deb" /pkg; done \
 && mkdir -p /ext/lib /ext/share/extension /ext/licenses \
 && cp /pkg/usr/lib/postgresql/${PG_MAJOR}/lib/timescaledb*.so /ext/lib/ \
 && cp /pkg/usr/share/postgresql/${PG_MAJOR}/extension/timescaledb* /ext/share/extension/ \
 && cp -r /pkg/usr/share/doc/. /ext/licenses/ \
 && curl -fsSL -o /ext/licenses/LICENSE-APACHE \
      "https://raw.githubusercontent.com/timescale/timescaledb/${TIMESCALEDB_VERSION}/LICENSE-APACHE" \
 && curl -fsSL -o /ext/licenses/LICENSE-TIMESCALE \
      "https://raw.githubusercontent.com/timescale/timescaledb/${TIMESCALEDB_VERSION}/tsl/LICENSE-TIMESCALE" \
 && for v in "${TIMESCALEDB_VERSION}" "${TIMESCALEDB_PREVIOUS_VERSION}"; do \
      test -f "/ext/lib/timescaledb-tsl-$v.so" && test -f "/ext/share/extension/timescaledb--$v.sql" || exit 1; \
    done \
 && grep -q "default_version = '${TIMESCALEDB_VERSION}'" /ext/share/extension/timescaledb.control \
 && ls -R /ext

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/maksimillian1/ha-multitenant-saas-platform" \
      org.opencontainers.image.description="TimescaleDB (TSL build, unmodified) as a CloudNativePG ImageVolume extension for PostgreSQL 18" \
      org.opencontainers.image.licenses="Apache-2.0 AND LicenseRef-Timescale"
COPY --from=fetch /ext/ /
