package server

import (
	"net"
	"testing"
	"time"

	"dns-forwarder/config"
	"dns-forwarder/internal/cache"
	"dns-forwarder/internal/dnssec"
	"dns-forwarder/internal/forwarder"
	"dns-forwarder/internal/overrides"
	"dns-forwarder/internal/ratelimit"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// startUpstream spins up a real DNS server acting as upstream.
func startUpstream(t *testing.T, handler func(dns.ResponseWriter, *dns.Msg)) (addr string, stop func()) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)
	srv := &dns.Server{PacketConn: pc, Net: "udp", Handler: mux}
	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }
	go srv.ActivateAndServe()
	<-ready
	return pc.LocalAddr().String(), func() { srv.Shutdown() }
}

func disabledLimiter(t *testing.T) *ratelimit.Limiter {
	t.Helper()
	l, err := ratelimit.New(config.RateLimitConfig{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func newHandler(t *testing.T, upstreamAddr string) *Handler {
	t.Helper()
	return newHandlerWithOverrides(t, upstreamAddr, nil)
}

func newHandlerWithOverrides(t *testing.T, upstreamAddr string, recs []overrides.Record) *Handler {
	t.Helper()
	c := cache.New(100, 5, 3600)
	f, err := forwarder.New([]string{upstreamAddr}, 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ovr, err := overrides.New(recs)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := dnssec.New("off", zap.NewNop())
	return NewHandler(c, f, ovr, v, false, disabledLimiter(t), zap.NewNop())
}

func doQuery(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	return resp
}

// — tests —

func TestHandlerForwardsAndCaches(t *testing.T) {
	hits := 0
	upAddr, stopUp := startUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		hits++
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("9.9.9.9"),
		}}
		w.WriteMsg(m)
	})
	defer stopUp()

	h := newHandler(t, upAddr)

	// bind the handler on a random port
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	srv := &dns.Server{PacketConn: pc, Net: "udp", Handler: h}
	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }
	go srv.ActivateAndServe()
	<-ready
	defer srv.Shutdown()

	srvAddr := pc.LocalAddr().String()

	// first query — should hit upstream
	resp1 := doQuery(t, srvAddr, "example.com", dns.TypeA)
	if resp1.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR, got %s", dns.RcodeToString[resp1.Rcode])
	}
	if hits != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", hits)
	}

	// second query — should come from cache
	resp2 := doQuery(t, srvAddr, "example.com", dns.TypeA)
	if resp2.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR, got %s", dns.RcodeToString[resp2.Rcode])
	}
	if hits != 1 {
		t.Fatalf("expected still 1 upstream hit (cache), got %d", hits)
	}
}

func TestHandlerServfailWhenUpstreamDown(t *testing.T) {
	// point to a port nobody listens on
	f, _ := forwarder.New([]string{"127.0.0.1:1"}, 200*time.Millisecond, nil)
	ovr, _ := overrides.New(nil)
	v, _ := dnssec.New("off", zap.NewNop())
	h := NewHandler(cache.New(100, 5, 3600), f, ovr, v, false, disabledLimiter(t), zap.NewNop())

	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	srv := &dns.Server{PacketConn: pc, Net: "udp", Handler: h}
	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }
	go srv.ActivateAndServe()
	<-ready
	defer srv.Shutdown()

	resp := doQuery(t, pc.LocalAddr().String(), "example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("expected SERVFAIL, got %s", dns.RcodeToString[resp.Rcode])
	}
}

func TestHandlerEmptyQuestion(t *testing.T) {
	upAddr, stopUp := startUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m)
	})
	defer stopUp()

	h := newHandler(t, upAddr)

	w := &fakeWriter{}
	r := new(dns.Msg) // no questions
	h.ServeDNS(w, r)

	if w.msg == nil || w.msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("expected FORMERR, got %v", w.msg)
	}
}

func TestHandlerUsesOverride(t *testing.T) {
	upstreamHits := 0
	upAddr, stopUp := startUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		upstreamHits++
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m)
	})
	defer stopUp()

	h := newHandlerWithOverrides(t, upAddr, []overrides.Record{
		{Name: "myservice.local.", Type: "A", TTL: 300, Value: "10.10.10.1"},
	})

	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	srv := &dns.Server{PacketConn: pc, Net: "udp", Handler: h}
	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }
	go srv.ActivateAndServe()
	<-ready
	defer srv.Shutdown()

	srvAddr := pc.LocalAddr().String()

	// Override name: must not reach upstream, must return configured IP.
	resp := doQuery(t, srvAddr, "myservice.local.", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR, got %s", dns.RcodeToString[resp.Rcode])
	}
	if upstreamHits != 0 {
		t.Fatalf("override should not reach upstream, got %d hits", upstreamHits)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer RR, got %d", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected *dns.A, got %T", resp.Answer[0])
	}
	if a.A.String() != "10.10.10.1" {
		t.Errorf("expected 10.10.10.1, got %s", a.A)
	}
	if !resp.Authoritative {
		t.Error("override response must be authoritative")
	}

	// Non-override name: must reach upstream.
	doQuery(t, srvAddr, "example.com.", dns.TypeA)
	if upstreamHits != 1 {
		t.Fatalf("non-override should hit upstream once, got %d", upstreamHits)
	}
}

// fakeWriter captures WriteMsg output without a real connection.
type fakeWriter struct {
	msg        *dns.Msg
	remoteAddr net.Addr // nil → &net.UDPAddr{} (backwards-compatible)
}

func (f *fakeWriter) LocalAddr() net.Addr { return &net.UDPAddr{} }
func (f *fakeWriter) RemoteAddr() net.Addr {
	if f.remoteAddr != nil {
		return f.remoteAddr
	}
	return &net.UDPAddr{}
}
func (f *fakeWriter) WriteMsg(m *dns.Msg) error   { f.msg = m; return nil }
func (f *fakeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeWriter) Close() error                { return nil }
func (f *fakeWriter) TsigStatus() error           { return nil }
func (f *fakeWriter) TsigTimersOnly(bool)         {}
func (f *fakeWriter) Hijack()                     {}

func TestHandlerRateLimited(t *testing.T) {
	upAddr, stopUp := startUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("1.1.1.1"),
		}}
		w.WriteMsg(m)
	})
	defer stopUp()

	// Use a very low refill rate so no tokens accumulate between test calls.
	rl, err := ratelimit.New(config.RateLimitConfig{
		Enabled:        true,
		RequestsPerSec: 0.001, // 1 token per ~17 minutes
		Burst:          2,
	})
	if err != nil {
		t.Fatal(err)
	}

	c := cache.New(100, 5, 3600)
	f, _ := forwarder.New([]string{upAddr}, 3*time.Second, nil)
	ovr, _ := overrides.New(nil)
	v, _ := dnssec.New("off", zap.NewNop())
	h := NewHandler(c, f, ovr, v, false, rl, zap.NewNop())

	clientAddr := &net.UDPAddr{IP: net.ParseIP("203.0.113.1"), Port: 9999}
	makeQuery := func() *dns.Msg {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)
		return m
	}

	// First two requests within burst — must succeed.
	for i := 0; i < 2; i++ {
		w := &fakeWriter{remoteAddr: clientAddr}
		h.ServeDNS(w, makeQuery())
		if w.msg.Rcode == dns.RcodeRefused {
			t.Fatalf("request %d should not be rate-limited (burst=2)", i+1)
		}
	}

	// Third request from the same IP — must be REFUSED.
	w3 := &fakeWriter{remoteAddr: clientAddr}
	h.ServeDNS(w3, makeQuery())
	if w3.msg.Rcode != dns.RcodeRefused {
		t.Fatalf("expected REFUSED after burst exhausted, got %s", dns.RcodeToString[w3.msg.Rcode])
	}

	// Different IP — must still be allowed.
	otherAddr := &net.UDPAddr{IP: net.ParseIP("203.0.113.2"), Port: 9999}
	w4 := &fakeWriter{remoteAddr: otherAddr}
	h.ServeDNS(w4, makeQuery())
	if w4.msg.Rcode == dns.RcodeRefused {
		t.Fatal("different IP should have an independent bucket and not be rate-limited")
	}
}
