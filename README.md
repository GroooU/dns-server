# dns-forwarder

A lightweight DNS forwarding server with in-memory caching, written in Go.

Resolves DNS queries by forwarding them to configurable upstream resolvers and caches responses according to their TTL. Supports encrypted transports (DoT, DoH), local record overrides, DNSSEC validation, and per-IP rate limiting.

## Features

- UDP + TCP on port 53
- Round-robin upstream selection with automatic failover
- Encrypted upstream transports: DNS-over-TLS (DoT) and DNS-over-HTTPS (DoH, RFC 8484)
- TTL-based in-memory cache with configurable min/max TTL clamping
- Local DNS overrides — static records served before cache and upstream
- DNSSEC validation — AD-bit trust mode or local RRSIG cryptographic verification
- Per-IP rate limiting — token bucket with CIDR allowlist
- Prometheus metrics + JSON stats endpoint
- Graceful shutdown on SIGINT/SIGTERM
- Configuration via YAML file or environment variables (`DNS_` prefix)

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

Requires Go 1.22+. Port 53 requires elevated privileges on Linux (`sudo` or `CAP_NET_BIND_SERVICE`).

## Build

```bash
# Binary
go build -o dns-forwarder ./cmd/server

# Docker image
docker build -t dns-forwarder:latest .
```

## Configuration

Configuration is loaded from `config.yaml` in the working directory. All values can be overridden via environment variables with the `DNS_` prefix (nested keys use `_` as separator, e.g. `DNS_CACHE_MAX_SIZE`).

```yaml
listen: "0.0.0.0:53"

# Upstream resolvers. Three URL schemes are supported:
#   host:port                 — plain UDP (default)
#   tls://host:port           — DNS-over-TLS (DoT)
#   https://host/dns-query    — DNS-over-HTTPS (DoH, RFC 8484)
upstreams:
  - "tls://8.8.8.8:853"    # Google DoT
  - "tls://1.1.1.1:853"    # Cloudflare DoT

# TLS settings for encrypted upstream connections.
tls:
  insecure_skip_verify: false  # set true only for self-signed test upstreams
  server_name: ""              # override SNI, e.g. "dns.google"

metrics:
  listen: "0.0.0.0:8053"

cache:
  max_size: 10000   # maximum number of cached entries
  min_ttl: 30       # clamp TTL up (seconds)
  max_ttl: 3600     # clamp TTL down (seconds)

log:
  level: "info"     # debug | info | warn | error

# Local DNS overrides — answered before cache and upstream forwarding.
overrides: []
# Example:
#   - name: "myservice.local."
#     type: "A"
#     ttl: 300
#     value: "192.168.1.100"

# DNSSEC validation.
#   off    — no DNSSEC (default)
#   ad     — trust the AD bit from validating upstreams (1.1.1.1, 8.8.8.8)
#   verify — local RRSIG cryptographic check (partial, no full chain of trust)
dnssec:
  mode: "off"
  block_bogus: false   # return SERVFAIL on Bogus result (most useful in verify mode)

# Per-client-IP rate limiting.
rate_limit:
  enabled: false
  requests_per_sec: 100  # token refill rate per IP
  burst: 20              # token bucket capacity (handles bursts)
  allowlist:             # CIDRs exempt from rate limiting
    - "127.0.0.0/8"
    - "10.0.0.0/8"
    - "172.16.0.0/12"
    - "192.168.0.0/16"
```

### Environment variables

| Variable | Example | Description |
|----------|---------|-------------|
| `DNS_LISTEN` | `0.0.0.0:53` | DNS listen address |
| `DNS_LOG_LEVEL` | `debug` | Log level |
| `DNS_CACHE_MAX_SIZE` | `50000` | Cache capacity |
| `DNS_CACHE_MIN_TTL` | `10` | Min TTL (seconds) |
| `DNS_CACHE_MAX_TTL` | `7200` | Max TTL (seconds) |
| `DNS_METRICS_LISTEN` | `0.0.0.0:9090` | Metrics listen address |
| `DNS_TLS_INSECURE_SKIP_VERIFY` | `true` | Skip TLS verification |
| `DNS_DNSSEC_MODE` | `ad` | DNSSEC mode |
| `DNS_DNSSEC_BLOCK_BOGUS` | `true` | SERVFAIL on Bogus |
| `DNS_RATE_LIMIT_ENABLED` | `true` | Enable rate limiting |
| `DNS_RATE_LIMIT_REQUESTS_PER_SEC` | `50` | QPS limit per IP |
| `DNS_RATE_LIMIT_BURST` | `10` | Burst capacity |

## Metrics

The HTTP server on port `8053` exposes three endpoints.

### `GET /health`

```json
{"status":"ok"}
```

### `GET /stats`

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

Prometheus text format.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `dns_requests_total` | Counter | `qtype`, `rcode`, `source` | Requests by type, code, and source (`cache`/`upstream`/`override`) |
| `dns_request_duration_seconds` | Histogram | `source` | End-to-end query latency |
| `dns_upstream_rtt_seconds` | Histogram | `protocol` (`udp`/`dot`/`doh`) | Upstream round-trip time |
| `dns_upstream_errors_total` | Counter | `protocol` | Upstream failures |
| `dns_cache_size` | Gauge | — | Current cache entry count |
| `dns_dnssec_validations_total` | Counter | `mode`, `result` | DNSSEC outcomes (`secure`/`insecure`/`bogus`/`indeterminate`) |
| `dns_rate_limited_total` | Counter | — | Requests rejected by rate limiter |

## Architecture

```
Client query (UDP/TCP :53)
  │
  ▼
Rate limiter — per-IP token bucket
  └── over limit? → REFUSED
  │
  ▼
Local overrides — static records (highest priority)
  └── match? → authoritative response
  │
  ▼
Cache — TTL-based in-memory
  └── hit? → response (DNSSEC RRs stripped if client didn't request them)
  │
  ▼
Forwarder — round-robin across upstreams (UDP / DoT / DoH)
  ├── success → DNSSEC validate (if enabled) → cache → response
  └── all fail → SERVFAIL
```

## Local overrides

Override any DNS name with a static record. Overrides are served before the cache and upstream, and the response is marked authoritative.

```yaml
overrides:
  - name: "api.internal."
    type: "A"
    ttl: 300
    value: "10.0.1.50"
  - name: "db.internal."
    type: "AAAA"
    ttl: 60
    value: "fd00::db"
  - name: "mail.internal."
    type: "MX"
    ttl: 300
    value: "10 smtp.internal."
```

## DNSSEC

Two validation modes are available:

**`ad` mode** — trusts the AD (Authenticated Data) bit returned by validating upstreams (1.1.1.1, 8.8.8.8 both do full DNSSEC validation). Recommended for a forwarder.

```yaml
dnssec:
  mode: "ad"
  block_bogus: false  # AD=0 is reported as Insecure, not Bogus (can't distinguish without local crypto)
```

**`verify` mode** — locally verifies RRSIG signatures against DNSKEY records in the response. Partial validation (no full chain of trust, no DS lookups). Set `block_bogus: true` to return SERVFAIL on failed signatures.

```yaml
dnssec:
  mode: "verify"
  block_bogus: true
```

## Rate limiting

Per-client-IP token bucket. Each IP gets an independent bucket that refills at `requests_per_sec` tokens/second up to `burst`. Clients over limit receive `REFUSED`.

```yaml
rate_limit:
  enabled: true
  requests_per_sec: 100
  burst: 20
  allowlist:
    - "127.0.0.0/8"     # loopback — never limited
    - "192.168.0.0/16"  # local network — never limited
```

Inactive IP entries are automatically evicted after 5 minutes.

## Testing

```bash
# Run all tests
go test ./...

# Test client (queries your local server)
go run ./cmd/query <name> [server] [qtype] [tcp]

# Examples:
go run ./cmd/query google.com 127.0.0.1:53 A
go run ./cmd/query cloudflare.com 127.0.0.1:53 AAAA
go run ./cmd/query github.com 127.0.0.1:53 MX tcp

# Check DNSSEC (requires mode=ad or mode=verify)
go run ./cmd/query dnssec-ok.org 127.0.0.1:53 A
```

## Project structure

```
dns-forwarder/
├── cmd/
│   ├── server/        # Main entrypoint
│   └── query/         # Test DNS client
├── config/            # Config loading (viper + env vars)
├── internal/
│   ├── cache/         # TTL cache with background eviction
│   ├── dnssec/        # DNSSEC validator (AD-bit and local RRSIG)
│   ├── forwarder/     # Upstream client: UDP, DoT, DoH; round-robin + retry
│   ├── metrics/       # Prometheus metrics definitions + HTTP server
│   ├── overrides/     # Local static DNS record overrides
│   ├── ratelimit/     # Per-IP token bucket rate limiter
│   └── server/        # DNS handler + UDP/TCP server
├── deploy/
│   ├── docker-compose.yml   # Full stack with Prometheus + Grafana
│   ├── prometheus.yml
│   ├── grafana/
│   └── terraform/           # Yandex Cloud infrastructure
├── config.yaml
└── Dockerfile
```

## Docker Compose

Full observability stack (DNS forwarder + Prometheus + Grafana):

```bash
cd deploy
docker compose up -d
```

Services: DNS on `:53`, metrics on `:8053`, Prometheus on `:9090`, Grafana on `:3000`.

Minimal example:

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
      DNS_DNSSEC_MODE: ad
      DNS_RATE_LIMIT_ENABLED: "true"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8053/health"]
      interval: 15s
      timeout: 3s
      retries: 3
```
