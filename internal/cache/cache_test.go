package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// helpers

func makeQuery(name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	return m
}

func makeResponse(name string, qtype uint16, ttl uint32) *dns.Msg {
	req := makeQuery(name, qtype)
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{
				Name:   dns.Fqdn(name),
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			A: []byte{1, 2, 3, 4},
		},
	}
	return resp
}

func newCache() *Cache {
	return New(100, 5, 3600)
}

// tests

func TestGetMiss(t *testing.T) {
	c := newCache()
	_, ok := c.Get(makeQuery("example.com", dns.TypeA))
	if ok {
		t.Fatal("expected cache miss")
	}
	if c.Stats().Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", c.Stats().Misses)
	}
}

func TestSetAndGet(t *testing.T) {
	c := newCache()
	resp := makeResponse("example.com", dns.TypeA, 300)
	c.Set(resp)

	got, ok := c.Get(makeQuery("example.com", dns.TypeA))
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(got.Answer))
	}
	if c.Stats().Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", c.Stats().Hits)
	}
}

func TestTTLDecreases(t *testing.T) {
	c := newCache()
	resp := makeResponse("example.com", dns.TypeA, 300)
	c.Set(resp)

	time.Sleep(2 * time.Second)

	got, ok := c.Get(makeQuery("example.com", dns.TypeA))
	if !ok {
		t.Fatal("expected hit after 2s")
	}
	ttl := got.Answer[0].Header().Ttl
	if ttl >= 300 {
		t.Fatalf("expected TTL < 300, got %d", ttl)
	}
}

func TestExpiry(t *testing.T) {
	c := New(100, 1, 3600)

	resp := makeResponse("example.com", dns.TypeA, 1)
	c.Set(resp)

	time.Sleep(2 * time.Second)

	_, ok := c.Get(makeQuery("example.com", dns.TypeA))
	if ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestMinTTLClamping(t *testing.T) {
	c := New(100, 60, 3600)
	resp := makeResponse("example.com", dns.TypeA, 5) // below minTTL
	c.Set(resp)

	e := c.entries[questionKey(resp.Question[0])]
	if e == nil {
		t.Fatal("entry not stored")
	}
	remaining := e.remainingTTL()
	if remaining < 55 {
		t.Fatalf("expected TTL clamped to ~60, got %d", remaining)
	}
}

func TestMaxTTLClamping(t *testing.T) {
	c := New(100, 5, 100)
	resp := makeResponse("example.com", dns.TypeA, 9999) // above maxTTL
	c.Set(resp)

	e := c.entries[questionKey(resp.Question[0])]
	if e == nil {
		t.Fatal("entry not stored")
	}
	remaining := e.remainingTTL()
	if remaining > 100 {
		t.Fatalf("expected TTL clamped to <=100, got %d", remaining)
	}
}

func TestDifferentQtypes(t *testing.T) {
	c := newCache()
	c.Set(makeResponse("example.com", dns.TypeA, 300))
	c.Set(makeResponse("example.com", dns.TypeAAAA, 300))

	if c.Stats().Size != 2 {
		t.Fatalf("expected 2 entries, got %d", c.Stats().Size)
	}

	_, okA := c.Get(makeQuery("example.com", dns.TypeA))
	_, okAAAA := c.Get(makeQuery("example.com", dns.TypeAAAA))
	if !okA || !okAAAA {
		t.Fatal("expected both A and AAAA to be cached")
	}
}

func TestMaxSizeEviction(t *testing.T) {
	c := New(3, 5, 3600)
	c.Set(makeResponse("a.com", dns.TypeA, 300))
	c.Set(makeResponse("b.com", dns.TypeA, 300))
	c.Set(makeResponse("c.com", dns.TypeA, 300))
	c.Set(makeResponse("d.com", dns.TypeA, 300)) // triggers eviction

	if c.Stats().Size > 3 {
		t.Fatalf("expected size <=3, got %d", c.Stats().Size)
	}
}

func TestDoNotCacheServerFail(t *testing.T) {
	c := newCache()
	req := makeQuery("example.com", dns.TypeA)
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = dns.RcodeServerFailure
	c.Set(resp)

	if c.Stats().Size != 0 {
		t.Fatal("SERVFAIL should not be cached")
	}
}

func TestResponseIDReplaced(t *testing.T) {
	c := newCache()
	c.Set(makeResponse("example.com", dns.TypeA, 300))

	req := makeQuery("example.com", dns.TypeA)
	req.Id = 0xABCD
	got, ok := c.Get(req)
	if !ok {
		t.Fatal("expected hit")
	}
	if got.Id != 0xABCD {
		t.Fatalf("expected response ID 0xABCD, got 0x%X", got.Id)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := newCache()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Set(makeResponse("example.com", dns.TypeA, 300))
		}()
		go func() {
			defer wg.Done()
			c.Get(makeQuery("example.com", dns.TypeA))
		}()
	}
	wg.Wait()
}
