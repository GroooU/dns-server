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

// Upstream round-trip time histogram.
var UpstreamDuration = promauto.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "dns_upstream_rtt_seconds",
		Help:    "Round-trip time to upstream resolvers.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
	},
)

// Upstream failures counter.
var UpstreamErrorsTotal = promauto.NewCounter(
	prometheus.CounterOpts{
		Name: "dns_upstream_errors_total",
		Help: "Total number of upstream resolver failures.",
	},
)

// Cache size gauge — updated on each stats scrape.
var CacheSize = promauto.NewGauge(
	prometheus.GaugeOpts{
		Name: "dns_cache_size",
		Help: "Current number of entries in the DNS cache.",
	},
)
