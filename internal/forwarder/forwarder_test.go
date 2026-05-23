package forwarder

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startMockServer starts a UDP DNS server on a random port.
// handler receives the request and returns a response.
func startMockServer(t *testing.T, handler func(w dns.ResponseWriter, r *dns.Msg)) (addr string, stop func()) {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &dns.Server{PacketConn: pc, Net: "udp"}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)
	srv.Handler = mux

	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }

	go srv.ActivateAndServe()
	<-ready

	return pc.LocalAddr().String(), func() { srv.Shutdown() }
}

func answerA(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{
				Name:   r.Question[0].Name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    60,
			},
			A: net.ParseIP("1.2.3.4"),
		},
	}
	w.WriteMsg(m)
}

func answerSERVFAIL(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Rcode = dns.RcodeServerFailure
	w.WriteMsg(m)
}

func makeQuery(name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	return m
}

// — tests —

func TestForwardSuccess(t *testing.T) {
	addr, stop := startMockServer(t, answerA)
	defer stop()

	f, _ := New([]string{addr}, 3*time.Second)
	resp, rtt, err := f.Forward(makeQuery("example.com"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answer))
	}
	if rtt < 0 {
		t.Fatal("expected non-negative RTT")
	}
}

func TestForwardRoundRobin(t *testing.T) {
	hits := make([]int, 2)

	addr0, stop0 := startMockServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		hits[0]++
		answerA(w, r)
	})
	addr1, stop1 := startMockServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		hits[1]++
		answerA(w, r)
	})
	defer stop0()
	defer stop1()

	f, _ := New([]string{addr0, addr1}, 3*time.Second)

	for i := 0; i < 4; i++ {
		_, _, err := f.Forward(makeQuery("example.com"))
		if err != nil {
			t.Fatalf("query %d failed: %v", i, err)
		}
	}

	if hits[0] != 2 || hits[1] != 2 {
		t.Fatalf("expected round-robin 2/2, got %d/%d", hits[0], hits[1])
	}
}

func TestForwardFallbackOnSERVFAIL(t *testing.T) {
	addr0, stop0 := startMockServer(t, answerSERVFAIL)
	addr1, stop1 := startMockServer(t, answerA)
	defer stop0()
	defer stop1()

	f, _ := New([]string{addr0, addr1}, 3*time.Second)
	resp, _, err := f.Forward(makeQuery("example.com"))
	if err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatal("expected answer from fallback upstream")
	}
}

func TestForwardFallbackOnTimeout(t *testing.T) {
	// upstream that never responds
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	deadAddr := pc.LocalAddr().String()

	addr1, stop1 := startMockServer(t, answerA)
	defer stop1()

	f, _ := New([]string{deadAddr, addr1}, 300*time.Millisecond)
	resp, _, err := f.Forward(makeQuery("example.com"))
	if err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatal("expected answer from fallback upstream")
	}
}

func TestForwardAllFailed(t *testing.T) {
	// two upstreams that both SERVFAIL
	addr0, stop0 := startMockServer(t, answerSERVFAIL)
	addr1, stop1 := startMockServer(t, answerSERVFAIL)
	defer stop0()
	defer stop1()

	f, _ := New([]string{addr0, addr1}, 3*time.Second)
	_, _, err := f.Forward(makeQuery("example.com"))
	if err == nil {
		t.Fatal("expected error when all upstreams fail")
	}
	if !errors.Is(err, ErrAllUpstreamsFailed) {
		t.Fatalf("expected ErrAllUpstreamsFailed, got: %v", err)
	}
}

func TestNewNoUpstreams(t *testing.T) {
	_, err := New([]string{}, time.Second)
	if err == nil {
		t.Fatal("expected error for empty upstreams")
	}
}
