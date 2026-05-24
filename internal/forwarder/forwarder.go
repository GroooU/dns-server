package forwarder

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

var ErrAllUpstreamsFailed = errors.New("all upstreams failed")

// Forwarder sends DNS queries to upstream resolvers with round-robin and retry.
type Forwarder struct {
	upstreams []Upstream
	timeout   time.Duration
	counter   atomic.Uint64
}

func New(rawURLs []string, timeout time.Duration, tlsCfg *tls.Config) (*Forwarder, error) {
	if len(rawURLs) == 0 {
		return nil, errors.New("at least one upstream is required")
	}
	upstreams := make([]Upstream, 0, len(rawURLs))
	for _, rawURL := range rawURLs {
		u, err := NewUpstream(rawURL, timeout, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream %q: %w", rawURL, err)
		}
		upstreams = append(upstreams, u)
	}
	return &Forwarder{
		upstreams: upstreams,
		timeout:   timeout,
	}, nil
}

// Forward sends the request to upstreams in round-robin order, trying each on failure.
// Returns the response, RTT, protocol of the successful upstream, and any error.
func (f *Forwarder) Forward(req *dns.Msg) (*dns.Msg, time.Duration, string, error) {
	n := uint64(len(f.upstreams))
	start := f.counter.Add(1) - 1

	var lastErr error
	for i := uint64(0); i < n; i++ {
		upstream := f.upstreams[(start+i)%n]

		ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
		resp, rtt, err := upstream.Exchange(ctx, req)
		cancel()

		if err != nil {
			lastErr = fmt.Errorf("upstream %s: %w", upstream, err)
			continue
		}
		// SERVFAIL from upstream — try next.
		if resp.Rcode == dns.RcodeServerFailure {
			lastErr = fmt.Errorf("upstream %s: SERVFAIL", upstream)
			continue
		}
		return resp, rtt, upstream.Protocol(), nil
	}

	return nil, 0, "", fmt.Errorf("%w: %v", ErrAllUpstreamsFailed, lastErr)
}
