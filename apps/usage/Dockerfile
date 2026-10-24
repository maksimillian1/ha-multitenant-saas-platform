FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /usage .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /usage /usage
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usage"]
