package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

type entry struct {
	msg       *dns.Msg
	expiresAt time.Time
}

func (e *entry) remainingTTL() uint32 {
	ttl := time.Until(e.expiresAt).Seconds()
	if ttl < 1 {
		return 0
	}
	return uint32(ttl)
}

// Stats holds cache performance counters.
type Stats struct {
	Size   int
	Hits   uint64
	Misses uint64
}

// Cache is a TTL-based in-memory DNS response cache.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*entry
	maxSize int
	minTTL  uint32
	maxTTL  uint32

	hits   atomic.Uint64
	misses atomic.Uint64
}

func New(maxSize, minTTL, maxTTL int) *Cache {
	c := &Cache{
		entries: make(map[string]*entry, maxSize),
		maxSize: maxSize,
		minTTL:  uint32(minTTL),
		maxTTL:  uint32(maxTTL),
	}
	return c
}

// Start launches the background cleanup goroutine.
// Call cancel() or close the done channel to stop it.
func (c *Cache) Start(done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.evictExpired()
			case <-done:
				return
			}
		}
	}()
}

// Get returns a cloned DNS message with adjusted TTLs, or false on miss/expiry.
func (c *Cache) Get(q *dns.Msg) (*dns.Msg, bool) {
	key := msgKey(q)

	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	remaining := e.remainingTTL()
	if remaining == 0 {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		c.misses.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return cloneWithTTL(e.msg, q.Id, remaining), true
}

// Set stores a DNS response in the cache, clamping TTL to [minTTL, maxTTL].
func (c *Cache) Set(resp *dns.Msg) {
	if resp == nil || len(resp.Question) == 0 {
		return
	}
	// Don't cache failures.
	if resp.Rcode != dns.RcodeSuccess && resp.Rcode != dns.RcodeNameError {
		return
	}

	ttl := c.clampTTL(minMsgTTL(resp))
	if ttl == 0 {
		return
	}

	key := questionKey(resp.Question[0])

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		c.evictOldestLocked()
	}

	c.entries[key] = &entry{
		msg:       resp.Copy(),
		expiresAt: time.Now().Add(time.Duration(ttl) * time.Second),
	}
}

func (c *Cache) Stats() Stats {
	c.mu.RLock()
	size := len(c.entries)
	c.mu.RUnlock()
	return Stats{
		Size:   size,
		Hits:   c.hits.Load(),
		Misses: c.misses.Load(),
	}
}

func (c *Cache) evictExpired() {
	now := time.Now()
	c.mu.Lock()
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}

// evictOldestLocked removes the soonest-to-expire entry. Must be called with mu held.
func (c *Cache) evictOldestLocked() {
	var oldest string
	var oldestTime time.Time
	for k, e := range c.entries {
		if oldest == "" || e.expiresAt.Before(oldestTime) {
			oldest = k
			oldestTime = e.expiresAt
		}
	}
	if oldest != "" {
		delete(c.entries, oldest)
	}
}

func (c *Cache) clampTTL(ttl uint32) uint32 {
	if ttl < c.minTTL {
		ttl = c.minTTL
	}
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}
	return ttl
}

// msgKey builds a cache key from the first question of a request.
func msgKey(q *dns.Msg) string {
	if len(q.Question) == 0 {
		return ""
	}
	return questionKey(q.Question[0])
}

func questionKey(q dns.Question) string {
	return fmt.Sprintf("%s:%d", q.Name, q.Qtype)
}

// minMsgTTL returns the smallest TTL across all RRs in the message.
func minMsgTTL(msg *dns.Msg) uint32 {
	var min uint32 = ^uint32(0)
	for _, rr := range append(append(msg.Answer, msg.Ns...), msg.Extra...) {
		if rr.Header().Rrtype == dns.TypeOPT {
			continue
		}
		if t := rr.Header().Ttl; t < min {
			min = t
		}
	}
	if min == ^uint32(0) {
		return 0
	}
	return min
}

// cloneWithTTL copies the message, sets new ID and overwrites all TTLs.
func cloneWithTTL(src *dns.Msg, id uint16, ttl uint32) *dns.Msg {
	m := src.Copy()
	m.Id = id
	for _, rr := range append(append(m.Answer, m.Ns...), m.Extra...) {
		if rr.Header().Rrtype != dns.TypeOPT {
			rr.Header().Ttl = ttl
		}
	}
	return m
}
