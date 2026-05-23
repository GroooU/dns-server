package server

import (
	"net"
	"testing"
	"time"

	"dns-forwarder/internal/cache"
	"dns-forwarder/internal/forwarder"

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

func newHandler(t *testing.T, upstreamAddr string) *Handler {
	t.Helper()
	c := cache.New(100, 5, 3600)
	f, err := forwarder.New([]string{upstreamAddr}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(c, f, zap.NewNop())
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
	f, _ := forwarder.New([]string{"127.0.0.1:1"}, 200*time.Millisecond)
	h := NewHandler(cache.New(100, 5, 3600), f, zap.NewNop())

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

// fakeWriter captures WriteMsg output without a real connection.
type fakeWriter struct {
	msg *dns.Msg
}

func (f *fakeWriter) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (f *fakeWriter) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (f *fakeWriter) WriteMsg(m *dns.Msg) error   { f.msg = m; return nil }
func (f *fakeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeWriter) Close() error                { return nil }
func (f *fakeWriter) TsigStatus() error           { return nil }
func (f *fakeWriter) TsigTimersOnly(bool)         {}
func (f *fakeWriter) Hijack()                     {}
