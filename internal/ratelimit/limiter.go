package ratelimit

import (
	"fmt"
	"net"
	"sync"
	"time"

	"dns-forwarder/config"
)

const (
	cleanupInterval = 1 * time.Minute
	entryTTL        = 5 * time.Minute
)

// ipEntry holds per-IP token bucket state.
type ipEntry struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// allow refills the bucket based on elapsed time and consumes one token.
// Returns true if the request is allowed.
func (e *ipEntry) allow(rate, burst float64) bool {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	elapsed := now.Sub(e.lastSeen).Seconds()
	e.tokens += elapsed * rate
	if e.tokens > burst {
		e.tokens = burst
	}
	e.lastSeen = now
	if e.tokens >= 1 {
		e.tokens--
		return true
	}
	return false
}

// Limiter enforces a per-IP token bucket rate limit with CIDR allowlist support.
type Limiter struct {
	rate      float64
	burst     float64
	entries   sync.Map // string(IP) → *ipEntry
	allowlist []*net.IPNet
	enabled   bool
}

// New creates a Limiter from config. Returns a disabled Limiter when cfg.Enabled is false.
func New(cfg config.RateLimitConfig) (*Limiter, error) {
	if !cfg.Enabled {
		return &Limiter{enabled: false}, nil
	}
	if cfg.RequestsPerSec <= 0 {
		return nil, fmt.Errorf("ratelimit: requests_per_sec must be > 0, got %v", cfg.RequestsPerSec)
	}
	if cfg.Burst <= 0 {
		return nil, fmt.Errorf("ratelimit: burst must be > 0, got %v", cfg.Burst)
	}

	nets := make([]*net.IPNet, 0, len(cfg.Allowlist))
	for _, cidr := range cfg.Allowlist {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("ratelimit: invalid allowlist CIDR %q: %w", cidr, err)
		}
		nets = append(nets, ipnet)
	}

	return &Limiter{
		rate:      cfg.RequestsPerSec,
		burst:     float64(cfg.Burst),
		allowlist: nets,
		enabled:   true,
	}, nil
}

// Enabled returns true when rate limiting is active.
func (l *Limiter) Enabled() bool { return l.enabled }

// Allow returns true if the request from addr should be processed.
// Allowlisted IPs always return true. Malformed addresses fail open.
func (l *Limiter) Allow(addr net.Addr) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return true // fail open on malformed address
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}

	for _, network := range l.allowlist {
		if network.Contains(ip) {
			return true
		}
	}

	now := time.Now()
	val, _ := l.entries.LoadOrStore(host, &ipEntry{
		tokens:   l.burst,
		lastSeen: now,
	})
	entry := val.(*ipEntry)
	return entry.allow(l.rate, l.burst)
}

// Start launches a background goroutine that periodically evicts inactive IP entries.
// The goroutine stops when done is closed.
func (l *Limiter) Start(done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				l.sweep()
			case <-done:
				return
			}
		}
	}()
}

func (l *Limiter) sweep() {
	cutoff := time.Now().Add(-entryTTL)
	l.entries.Range(func(key, val any) bool {
		e := val.(*ipEntry)
		e.mu.Lock()
		idle := e.lastSeen.Before(cutoff)
		e.mu.Unlock()
		if idle {
			l.entries.Delete(key)
		}
		return true
	})
}
