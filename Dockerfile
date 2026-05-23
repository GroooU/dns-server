FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o dns-forwarder ./cmd/server

# ---

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/dns-forwarder .
COPY config.yaml .

EXPOSE 53/udp
EXPOSE 53/tcp
EXPOSE 8053/tcp

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
    CMD wget -qO- http://localhost:8053/health || exit 1

ENTRYPOINT ["./dns-forwarder"]
