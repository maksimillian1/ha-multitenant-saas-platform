# The build stages run on the builder's platform: the SPA is the same for every
# target, and Go cross-compiles, so `docker buildx --platform
# linux/amd64,linux/arm64` needs no emulation (C-06).
FROM --platform=$BUILDPLATFORM node:22-slim AS spa
WORKDIR /app
COPY ui/app/package.json ui/app/package-lock.json ./
RUN npm ci
COPY ui/app/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# ui.go embeds ui/app/dist into the binary.
COPY --from=spa /app/dist ./ui/app/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /admin .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /admin /admin
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/admin"]
