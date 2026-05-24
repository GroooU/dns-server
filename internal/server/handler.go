package server

import (
	"errors"
	"time"

	"dns-forwarder/internal/cache"
	"dns-forwarder/internal/dnssec"
	"dns-forwarder/internal/forwarder"
	"dns-forwarder/internal/metrics"
	"dns-forwarder/internal/overrides"
	"dns-forwarder/internal/ratelimit"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

type Handler struct {
	cache      *cache.Cache
	forwarder  *forwarder.Forwarder
	overrides  *overrides.Resolver
	validator  *dnssec.Validator
	blockBogus bool
	limiter    *ratelimit.Limiter
	log        *zap.Logger
}

func NewHandler(
	c *cache.Cache,
	f *forwarder.Forwarder,
	ovr *overrides.Resolver,
	v *dnssec.Validator,
	blockBogus bool,
	limiter *ratelimit.Limiter,
	log *zap.Logger,
) *Handler {
	return &Handler{
		cache:      c,
		forwarder:  f,
		overrides:  ovr,
		validator:  v,
		blockBogus: blockBogus,
		limiter:    limiter,
		log:        log,
	}
}

func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()

	if len(r.Question) == 0 {
		h.writeError(w, r, dns.RcodeFormatError)
		return
	}

	if h.limiter.Enabled() && !h.limiter.Allow(w.RemoteAddr()) {
		metrics.RateLimitedTotal.Inc()
		h.log.Debug("rate limited", zap.String("addr", w.RemoteAddr().String()))
		h.writeError(w, r, dns.RcodeRefused)
		return
	}

	q := r.Question[0]

	qtype := dns.TypeToString[q.Qtype]

	if rrs, ok := h.overrides.Lookup(q); ok {
		resp := new(dns.Msg)
		resp.SetReply(r)
		resp.Authoritative = true
		resp.Answer = append([]dns.RR(nil), rrs...)
		_ = w.WriteMsg(resp)
		elapsed := time.Since(start)
		metrics.RequestsTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess], "override").Inc()
		metrics.RequestDuration.WithLabelValues("override").Observe(elapsed.Seconds())
		h.log.Debug("override hit",
			zap.String("name", q.Name),
			zap.String("type", qtype),
			zap.Duration("elapsed", elapsed),
		)
		return
	}

	if cached, ok := h.cache.Get(r); ok {
		toSend := cached
		if h.validator.Enabled() && !clientWantsDO(r) {
			toSend = cached.Copy()
			dnssec.StripDNSSECRRs(toSend)
		}
		_ = w.WriteMsg(toSend)
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

	resp, rtt, protocol, err := h.forwarder.Forward(r)
	if err != nil {
		metrics.UpstreamErrorsTotal.WithLabelValues("unknown").Inc()
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

	if h.validator.Enabled() {
		result := h.validator.Validate(r, resp)
		metrics.DNSSECValidationsTotal.
			WithLabelValues(modeLabel(h.validator.Mode()), result.String()).Inc()
		if result == dnssec.ResultBogus {
			h.log.Warn("dnssec bogus response",
				zap.String("name", q.Name),
				zap.String("type", qtype),
				zap.String("mode", modeLabel(h.validator.Mode())),
			)
			if h.blockBogus {
				h.writeError(w, r, dns.RcodeServerFailure)
				return
			}
		}
	}

	// Cache the full response (with RRSIG records if present).
	h.cache.Set(resp)

	toSend := resp
	if h.validator.Enabled() && !clientWantsDO(r) {
		toSend = resp.Copy()
		dnssec.StripDNSSECRRs(toSend)
	}
	_ = w.WriteMsg(toSend)

	elapsed := time.Since(start)
	rcode := dns.RcodeToString[resp.Rcode]
	metrics.RequestsTotal.WithLabelValues(qtype, rcode, "upstream").Inc()
	metrics.RequestDuration.WithLabelValues("upstream").Observe(elapsed.Seconds())
	metrics.UpstreamDuration.WithLabelValues(protocol).Observe(rtt.Seconds())
	h.log.Debug("forwarded",
		zap.String("name", q.Name),
		zap.String("type", qtype),
		zap.String("rcode", rcode),
		zap.String("protocol", protocol),
		zap.Duration("upstream_rtt", rtt),
		zap.Duration("elapsed", elapsed),
	)
}

func (h *Handler) writeError(w dns.ResponseWriter, r *dns.Msg, rcode int) {
	m := new(dns.Msg)
	m.SetRcode(r, rcode)
	_ = w.WriteMsg(m)
}

func clientWantsDO(r *dns.Msg) bool {
	opt := r.IsEdns0()
	return opt != nil && opt.Do()
}

func modeLabel(m dnssec.Mode) string {
	switch m {
	case dnssec.ModeAD:
		return "ad"
	case dnssec.ModeVerify:
		return "verify"
	default:
		return "off"
	}
}
