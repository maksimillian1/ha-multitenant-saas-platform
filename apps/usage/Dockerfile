# One Dockerfile, one image per binary: build with --target writeapi, writer or
# migrate (docker-bake.hcl builds all three). BuildKit builds only the stages
# the target needs, and each final image holds one binary.
#
# The build stages run on the builder's platform and cross-compile for the
# target, so `docker buildx --platform linux/amd64,linux/arm64` needs no
# emulation (C-06).
FROM --platform=$BUILDPLATFORM golang:1.26 AS base
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/

# The shared build cache compiles the common packages once for all three.
FROM base AS build-writeapi
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/writeapi

FROM base AS build-writer
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/writer

FROM base AS build-migrate
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot AS writeapi
COPY --from=build-writeapi /out/app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]

# Listens on 8080 for /healthz only.
FROM gcr.io/distroless/static-debian12:nonroot AS writer
COPY --from=build-writer /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]

FROM gcr.io/distroless/static-debian12:nonroot AS migrate
COPY --from=build-migrate /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]

# The last stage is the default without --target: fail instead of shipping
# whichever binary happens to come last.
FROM busybox:1.37 AS no-target
RUN echo "build with --target writeapi, writer or migrate" && exit 1
