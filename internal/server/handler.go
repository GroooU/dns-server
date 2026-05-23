package server

import (
	"errors"
	"time"

	"dns-forwarder/internal/cache"
	"dns-forwarder/internal/forwarder"
	"dns-forwarder/internal/metrics"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

type Handler struct {
	cache     *cache.Cache
	forwarder *forwarder.Forwarder
	log       *zap.Logger
}

func NewHandler(c *cache.Cache, f *forwarder.Forwarder, log *zap.Logger) *Handler {
	return &Handler{cache: c, forwarder: f, log: log}
}

func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()

	if len(r.Question) == 0 {
		h.writeError(w, r, dns.RcodeFormatError)
		return
	}

	q := r.Question[0]

	qtype := dns.TypeToString[q.Qtype]

	if cached, ok := h.cache.Get(r); ok {
		_ = w.WriteMsg(cached)
		elapsed := time.Since(start)
		metrics.RequestsTotal.WithLabelValues(qtype, dns.RcodeToString[cached.Rcode], "cache").Inc()
		metrics.RequestDuration.WithLabelValues("cache").Observe(elapsed.Seconds())
		h.log.Debug("cache hit",
			zap.String("name", q.Name),
			zap.String("type", qtype),
			zap.Duration("elapsed", elapsed),
		)
		return
	}

	resp, rtt, err := h.forwarder.Forward(r)
	if err != nil {
		metrics.UpstreamErrorsTotal.Inc()
		metrics.RequestsTotal.WithLabelValues(qtype, "SERVFAIL", "upstream").Inc()
		if errors.Is(err, forwarder.ErrAllUpstreamsFailed) {
			h.log.Warn("all upstreams failed",
				zap.String("name", q.Name),
				zap.Error(err),
			)
		}
		h.writeError(w, r, dns.RcodeServerFailure)
		return
	}

	h.cache.Set(resp)
	_ = w.WriteMsg(resp)

	elapsed := time.Since(start)
	rcode := dns.RcodeToString[resp.Rcode]
	metrics.RequestsTotal.WithLabelValues(qtype, rcode, "upstream").Inc()
	metrics.RequestDuration.WithLabelValues("upstream").Observe(elapsed.Seconds())
	metrics.UpstreamDuration.Observe(rtt.Seconds())
	h.log.Debug("forwarded",
		zap.String("name", q.Name),
		zap.String("type", qtype),
		zap.String("rcode", rcode),
		zap.Duration("upstream_rtt", rtt),
		zap.Duration("elapsed", elapsed),
	)
}

func (h *Handler) writeError(w dns.ResponseWriter, r *dns.Msg, rcode int) {
	m := new(dns.Msg)
	m.SetRcode(r, rcode)
	_ = w.WriteMsg(m)
}
