# The build stage runs on the builder's platform and cross-compiles for the
# target, so `docker buildx --platform linux/amd64,linux/arm64` needs no
# emulation (C-06).
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /exampleapp .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /exampleapp /exampleapp
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/exampleapp"]
CMD ["serve"]
