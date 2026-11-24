FROM golang:1.26.11-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/connections ./cmd/connections
COPY internal ./internal
COPY relay/migrations ./relay/migrations
RUN CGO_ENABLED=0 go build -trimpath -o /connections ./cmd/connections

FROM alpine:3.23
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /connections /connections
USER 65532:65532
CMD ["/connections", "-railway"]
