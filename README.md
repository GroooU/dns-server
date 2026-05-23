# dns-forwarder

A lightweight DNS forwarding server with in-memory caching, written in Go.

Resolves DNS queries by forwarding them to upstream resolvers (e.g. 8.8.8.8, 1.1.1.1) and caches responses according to their TTL. Repeated queries are served from cache without hitting upstream.

## Features

- UDP + TCP on port 53
- Round-robin upstream selection with automatic failover
- TTL-based in-memory cache with configurable min/max TTL clamping
- Prometheus metrics + JSON stats endpoint
- Graceful shutdown on SIGINT/SIGTERM
- Configuration via YAML file or environment variables

## Quick start

### Docker

```bash
docker run -d \
  --name dns-forwarder \
  -p 53:53/udp \
  -p 53:53/tcp \
  -p 8053:8053 \
  dns-forwarder:latest
```

### From source

```bash
git clone <repo>
cd dns-forwarder
go run ./cmd/server
```

Requires Go 1.25+. Port 53 requires elevated privileges on Linux (`sudo` or `CAP_NET_BIND_SERVICE`).

## Build

```bash
# Binary
go build -o dns-forwarder ./cmd/server

# Docker image
docker build -t dns-forwarder:latest .
```

## Configuration

Configuration is loaded from `config.yaml` in the working directory. All values can be overridden via environment variables with the `DNS_` prefix.

```yaml
listen: "0.0.0.0:53"       # DNS listen address (UDP + TCP)

upstreams:
  - "8.8.8.8:53"            # Google
  - "1.1.1.1:53"            # Cloudflare

metrics:
  listen: "0.0.0.0:8053"   # HTTP metrics server

cache:
  max_size: 10000           # Maximum number of cached entries
  min_ttl: 30               # Minimum TTL in seconds (clamps low values up)
  max_ttl: 3600             # Maximum TTL in seconds (clamps high values down)

log:
  level: "info"             # debug | info | warn | error
```

### Environment variables

Nested keys use `_` as separator. Prefix: `DNS_`.

| Variable | Example | Description |
|----------|---------|-------------|
| `DNS_LISTEN` | `0.0.0.0:53` | DNS listen address |
| `DNS_LOG_LEVEL` | `debug` | Log level |
| `DNS_CACHE_MAX_SIZE` | `50000` | Cache capacity |
| `DNS_CACHE_MIN_TTL` | `10` | Min TTL (seconds) |
| `DNS_CACHE_MAX_TTL` | `7200` | Max TTL (seconds) |
| `DNS_METRICS_LISTEN` | `0.0.0.0:9090` | Metrics listen address |

## Metrics

The HTTP server on port `8053` exposes three endpoints.

### `GET /health`

```json
{"status":"ok"}
```

### `GET /stats`

Human-readable JSON snapshot.

```json
{
  "cache": {
    "size": 42,
    "hits": 1337,
    "misses": 58,
    "hit_rate": "95.84%"
  },
  "time": "2026-05-23T10:00:00Z"
}
```

### `GET /metrics`

Prometheus text format. Key metrics:

| Metric | Type | Labels |
|--------|------|--------|
| `dns_requests_total` | Counter | `qtype`, `rcode`, `source` (cache/upstream) |
| `dns_request_duration_seconds` | Histogram | `source` |
| `dns_upstream_rtt_seconds` | Histogram | — |
| `dns_upstream_errors_total` | Counter | — |
| `dns_cache_size` | Gauge | — |

## Testing

```bash
# Run all tests
go test ./...

# With verbose output
go test ./... -v

# Test client (queries your local server)
go run ./cmd/query <name> [server] [qtype] [tcp]

# Examples:
go run ./cmd/query google.com 127.0.0.1:53 A
go run ./cmd/query github.com 127.0.0.1:53 AAAA tcp
```

## Project structure

```
dns-forwarder/
├── cmd/
│   ├── server/        # Main entrypoint
│   └── query/         # Test DNS client
├── config/            # Config loading (viper)
├── internal/
│   ├── cache/         # TTL cache (sync.RWMutex + background eviction)
│   ├── forwarder/     # Upstream client (round-robin + retry)
│   ├── metrics/       # Prometheus metrics + HTTP server
│   └── server/        # DNS handler + UDP/TCP server
├── config.yaml
└── Dockerfile
```

## Architecture

```
Client
  │
  ▼  UDP/TCP :53
Handler
  ├── Cache hit?  ──yes──► return (TTL adjusted)
  └── no
       │
       ▼
  Forwarder (round-robin)
  ├── upstream[0] ──ok──► cache + return
  ├── upstream[0] fails → upstream[1]
  └── all fail ──────────► SERVFAIL
```

## Docker Compose example

```yaml
services:
  dns-forwarder:
    image: dns-forwarder:latest
    ports:
      - "53:53/udp"
      - "53:53/tcp"
      - "8053:8053"
    environment:
      DNS_LOG_LEVEL: info
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8053/health"]
      interval: 15s
      timeout: 3s
      retries: 3
```
