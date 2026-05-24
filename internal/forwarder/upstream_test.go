package forwarder

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// — helpers —

func selfSignedTLSConfig(t *testing.T) (serverTLS *tls.Config, clientTLS *tls.Config) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}
	parsed, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)

	serverTLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	clientTLS = &tls.Config{RootCAs: pool}
	return
}

// startTLSMockServer starts a DNS-over-TLS server on a random port.
func startTLSMockServer(t *testing.T, handler func(dns.ResponseWriter, *dns.Msg)) (addr string, clientTLS *tls.Config, stop func()) {
	t.Helper()
	serverTLS, clientTLS := selfSignedTLSConfig(t)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}

	srv := &dns.Server{Listener: ln, Net: "tcp-tls"}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)
	srv.Handler = mux

	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }
	go srv.ActivateAndServe()
	<-ready

	return ln.Addr().String(), clientTLS, func() { srv.Shutdown() }
}

// — NewUpstream parsing tests —

func TestNewUpstreamParsing(t *testing.T) {
	timeout := time.Second

	cases := []struct {
		rawURL      string
		wantProto   string
		wantErrFrag string
	}{
		{"8.8.8.8:53", "udp", ""},
		{"1.1.1.1", "udp", ""},
		{"tls://8.8.8.8:853", "dot", ""},
		{"tls://8.8.8.8", "dot", ""},
		{"https://dns.google/dns-query", "doh", ""},
		{"ftp://bad.example", "", "unsupported upstream scheme"},
	}

	for _, tc := range cases {
		u, err := NewUpstream(tc.rawURL, timeout, nil)
		if tc.wantErrFrag != "" {
			if err == nil {
				t.Errorf("NewUpstream(%q): expected error containing %q, got nil", tc.rawURL, tc.wantErrFrag)
			}
			continue
		}
		if err != nil {
			t.Errorf("NewUpstream(%q): unexpected error: %v", tc.rawURL, err)
			continue
		}
		if u.Protocol() != tc.wantProto {
			t.Errorf("NewUpstream(%q): Protocol() = %q, want %q", tc.rawURL, u.Protocol(), tc.wantProto)
		}
	}
}

// — UDP upstream tests —

func TestUDPUpstreamExchange(t *testing.T) {
	addr, stop := startMockServer(t, answerA)
	defer stop()

	u := newUDPUpstream(addr, 3*time.Second)
	resp, rtt, err := u.Exchange(context.Background(), makeQuery("example.com"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answer))
	}
	if rtt <= 0 {
		t.Fatal("expected positive RTT")
	}
	if u.Protocol() != "udp" {
		t.Fatalf("Protocol() = %q, want udp", u.Protocol())
	}
}

func TestUDPUpstreamConnectionRefused(t *testing.T) {
	// Bind a port and close it immediately so connections are refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	u := newUDPUpstream(addr, 300*time.Millisecond)
	_, _, err = u.Exchange(context.Background(), makeQuery("example.com"))
	if err == nil {
		t.Fatal("expected error for unreachable upstream")
	}
}

// — DoT upstream tests —

func TestDotUpstreamExchange(t *testing.T) {
	addr, clientTLS, stop := startTLSMockServer(t, answerA)
	defer stop()

	u := newDotUpstream(addr, 3*time.Second, clientTLS)
	resp, rtt, err := u.Exchange(context.Background(), makeQuery("example.com"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answer))
	}
	if rtt <= 0 {
		t.Fatal("expected positive RTT")
	}
	if u.Protocol() != "dot" {
		t.Fatalf("Protocol() = %q, want dot", u.Protocol())
	}
}

func TestDotUpstreamTLSFailure(t *testing.T) {
	// Start TLS server with self-signed cert; client does NOT trust it.
	addr, _, stop := startTLSMockServer(t, answerA)
	defer stop()

	u := newDotUpstream(addr, 3*time.Second, nil) // nil = system roots, won't trust self-signed
	_, _, err := u.Exchange(context.Background(), makeQuery("example.com"))
	if err == nil {
		t.Fatal("expected TLS verification error")
	}
}

func TestDotUpstreamDefaultPort(t *testing.T) {
	// Verify that "tls://host" (no port) gets port 853 appended.
	u := newDotUpstream("dns.google", time.Second, nil)
	if u.addr != "dns.google:853" {
		t.Fatalf("expected addr dns.google:853, got %q", u.addr)
	}
}

// — DoH upstream tests —

// dohTestHandler serves application/dns-message responses over HTTPS.
func dohTestHandler(response *dns.Msg) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "bad content-type", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		wire, _ := response.Pack()
		w.Write(wire)
	}
}

func TestDohUpstreamExchange(t *testing.T) {
	reply := new(dns.Msg)
	reply.SetReply(makeQuery("example.com"))
	reply.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("1.2.3.4"),
		},
	}

	srv := httptest.NewTLSServer(dohTestHandler(reply))
	defer srv.Close()

	// Inject the test server's TLS client so the upstream trusts the cert.
	tlsCfg := &tls.Config{InsecureSkipVerify: true}
	u := newDohUpstream(srv.URL+"/dns-query", 3*time.Second, tlsCfg)

	resp, rtt, err := u.Exchange(context.Background(), makeQuery("example.com"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answer))
	}
	if rtt <= 0 {
		t.Fatal("expected positive RTT")
	}
	if u.Protocol() != "doh" {
		t.Fatalf("Protocol() = %q, want doh", u.Protocol())
	}
}

func TestDohUpstreamNon200(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tlsCfg := &tls.Config{InsecureSkipVerify: true}
	u := newDohUpstream(srv.URL+"/dns-query", 3*time.Second, tlsCfg)
	_, _, err := u.Exchange(context.Background(), makeQuery("example.com"))
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestDohUpstreamMalformedBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write([]byte("not a dns message"))
	}))
	defer srv.Close()

	tlsCfg := &tls.Config{InsecureSkipVerify: true}
	u := newDohUpstream(srv.URL+"/dns-query", 3*time.Second, tlsCfg)
	_, _, err := u.Exchange(context.Background(), makeQuery("example.com"))
	if err == nil {
		t.Fatal("expected error on malformed DNS response body")
	}
}
