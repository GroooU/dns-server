package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// DNS request labels: qtype, rcode, source (cache | upstream)
var RequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "dns_requests_total",
		Help: "Total number of DNS requests handled.",
	},
	[]string{"qtype", "rcode", "source"},
)

// Request latency histogram.
var RequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "dns_request_duration_seconds",
		Help:    "DNS request processing time.",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .05, .1, .5},
	},
	[]string{"source"},
)

// Upstream round-trip time histogram, broken down by protocol (udp/dot/doh).
var UpstreamDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "dns_upstream_rtt_seconds",
		Help:    "Round-trip time to upstream resolvers.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
	},
	[]string{"protocol"},
)

// Upstream failures counter, broken down by protocol.
var UpstreamErrorsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "dns_upstream_errors_total",
		Help: "Total number of upstream resolver failures.",
	},
	[]string{"protocol"},
)

// Cache size gauge — updated on each stats scrape.
var CacheSize = promauto.NewGauge(
	prometheus.GaugeOpts{
		Name: "dns_cache_size",
		Help: "Current number of entries in the DNS cache.",
	},
)

// RateLimitedTotal counts requests rejected by the per-IP rate limiter.
// No IP label — avoids high-cardinality time series.
var RateLimitedTotal = promauto.NewCounter(
	prometheus.CounterOpts{
		Name: "dns_rate_limited_total",
		Help: "Total DNS requests rejected due to per-IP rate limiting.",
	},
)

// DNSSECValidationsTotal counts DNSSEC validation outcomes by mode and result.
// Labels: mode = "ad" | "verify"; result = "secure" | "insecure" | "bogus" | "indeterminate"
var DNSSECValidationsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "dns_dnssec_validations_total",
		Help: "Total DNSSEC validation outcomes by mode and result.",
	},
	[]string{"mode", "result"},
)
