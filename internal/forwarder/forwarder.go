package forwarder

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

var ErrAllUpstreamsFailed = errors.New("all upstreams failed")

// Forwarder sends DNS queries to upstream resolvers with round-robin and retry.
type Forwarder struct {
	upstreams []string
	client    *dns.Client
	counter   atomic.Uint64
}

func New(upstreams []string, timeout time.Duration) (*Forwarder, error) {
	if len(upstreams) == 0 {
		return nil, errors.New("at least one upstream is required")
	}
	return &Forwarder{
		upstreams: upstreams,
		client: &dns.Client{
			Net:     "udp",
			Timeout: timeout,
		},
	}, nil
}

// Forward sends the request to upstreams in round-robin order, trying each on failure.
func (f *Forwarder) Forward(req *dns.Msg) (*dns.Msg, time.Duration, error) {
	n := uint64(len(f.upstreams))
	start := f.counter.Add(1) - 1

	var lastErr error
	for i := uint64(0); i < n; i++ {
		upstream := f.upstreams[(start+i)%n]

		resp, rtt, err := f.client.Exchange(req, upstream)
		if err != nil {
			lastErr = fmt.Errorf("upstream %s: %w", upstream, err)
			continue
		}
		// SERVFAIL from upstream — try next
		if resp.Rcode == dns.RcodeServerFailure {
			lastErr = fmt.Errorf("upstream %s: SERVFAIL", upstream)
			continue
		}
		return resp, rtt, nil
	}

	return nil, 0, fmt.Errorf("%w: %v", ErrAllUpstreamsFailed, lastErr)
}
