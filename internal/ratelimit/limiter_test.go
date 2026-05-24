package ratelimit

import (
	"net"
	"testing"
	"time"

	"dns-forwarder/config"
)

func cfg(enabled bool, rps float64, burst int, allowlist ...string) config.RateLimitConfig {
	return config.RateLimitConfig{
		Enabled:        enabled,
		RequestsPerSec: rps,
		Burst:          burst,
		Allowlist:      allowlist,
	}
}

func udpAddr(ip string) net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: 53}
}

func TestDisabledLimiterNotEnabled(t *testing.T) {
	l, err := New(cfg(false, 100, 10))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l.Enabled() {
		t.Fatal("disabled limiter must not be enabled")
	}
}

func TestBurstExhausted(t *testing.T) {
	const burst = 3
	l, _ := New(cfg(true, 1000, burst))
	addr := udpAddr("1.2.3.4")
	for i := 0; i < burst; i++ {
		if !l.Allow(addr) {
			t.Fatalf("request %d should be allowed (within burst=%d)", i+1, burst)
		}
	}
	if l.Allow(addr) {
		t.Fatal("request beyond burst should be rejected")
	}
}

func TestTokenRefill(t *testing.T) {
	l, _ := New(cfg(true, 10, 10))
	addr := udpAddr("2.3.4.5")
	for i := 0; i < 10; i++ {
		l.Allow(addr) // exhaust bucket
	}
	if l.Allow(addr) {
		t.Fatal("should be rejected after bucket exhausted")
	}
	time.Sleep(110 * time.Millisecond) // 10 tokens/s → ~1.1 tokens in 110ms
	if !l.Allow(addr) {
		t.Fatal("should have refilled at least 1 token after 110ms at 10 rps")
	}
}

func TestAllowlistBypassesLimit(t *testing.T) {
	l, _ := New(cfg(true, 1, 1, "10.0.0.0/8"))
	addr := udpAddr("10.1.2.3")
	for i := 0; i < 100; i++ {
		if !l.Allow(addr) {
			t.Fatalf("allowlisted IP must never be rejected (request %d)", i+1)
		}
	}
}

func TestNonAllowlistIsRateLimited(t *testing.T) {
	l, _ := New(cfg(true, 1, 1, "10.0.0.0/8"))
	addr := udpAddr("8.8.8.8")
	l.Allow(addr) // consume the 1 burst token
	if l.Allow(addr) {
		t.Fatal("non-allowlisted IP should be rate-limited after burst exhausted")
	}
}

func TestInvalidCIDRReturnsError(t *testing.T) {
	_, err := New(cfg(true, 100, 10, "not-a-cidr"))
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestSweepEvictsStaleEntries(t *testing.T) {
	l, _ := New(cfg(true, 100, 10))
	addr := udpAddr("3.3.3.3")
	l.Allow(addr) // creates the entry

	val, ok := l.entries.Load("3.3.3.3")
	if !ok {
		t.Fatal("entry should have been created after Allow()")
	}
	e := val.(*ipEntry)
	e.mu.Lock()
	e.lastSeen = time.Now().Add(-(entryTTL + time.Second))
	e.mu.Unlock()

	l.sweep()

	if _, exists := l.entries.Load("3.3.3.3"); exists {
		t.Fatal("stale entry should have been evicted by sweep()")
	}
}

func TestSweepKeepsActiveEntries(t *testing.T) {
	l, _ := New(cfg(true, 100, 10))
	addr := udpAddr("4.4.4.4")
	l.Allow(addr) // recent lastSeen

	l.sweep()

	if _, exists := l.entries.Load("4.4.4.4"); !exists {
		t.Fatal("recently active entry must not be evicted")
	}
}

func TestDifferentIPsAreIndependent(t *testing.T) {
	l, _ := New(cfg(true, 1000, 2))
	addr1 := udpAddr("5.5.5.5")
	addr2 := udpAddr("6.6.6.6")

	l.Allow(addr1)
	l.Allow(addr1) // exhaust addr1

	if !l.Allow(addr2) {
		t.Fatal("addr2 bucket should be independent and not exhausted")
	}
}
