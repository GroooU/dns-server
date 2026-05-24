package forwarder

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// Upstream is a DNS upstream resolver.
type Upstream interface {
	Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, error)
	String() string
	Protocol() string // "udp", "dot", or "doh"
}

// NewUpstream parses rawURL and returns the appropriate Upstream implementation.
//
// Supported formats:
//   - "host:port" or "host"         — plain UDP
//   - "tls://host:port" or "tls://host" — DNS-over-TLS (DoT), default port 853
//   - "https://host/path"           — DNS-over-HTTPS (DoH, RFC 8484)
func NewUpstream(rawURL string, timeout time.Duration, tlsCfg *tls.Config) (Upstream, error) {
	if idx := strings.Index(rawURL, "://"); idx != -1 {
		scheme := rawURL[:idx]
		rest := rawURL[idx+3:]
		switch scheme {
		case "tls":
			return newDotUpstream(rest, timeout, tlsCfg), nil
		case "https":
			return newDohUpstream(rawURL, timeout, tlsCfg), nil
		default:
			return nil, fmt.Errorf("unsupported upstream scheme %q in %q", scheme, rawURL)
		}
	}
	return newUDPUpstream(rawURL, timeout), nil
}

// --- UDP ---

type udpUpstream struct {
	addr   string
	client *dns.Client
}

func newUDPUpstream(addr string, timeout time.Duration) *udpUpstream {
	return &udpUpstream{
		addr: addr,
		client: &dns.Client{
			Net:     "udp",
			Timeout: timeout,
		},
	}
}

func (u *udpUpstream) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, error) {
	return u.client.ExchangeContext(ctx, msg, u.addr)
}

func (u *udpUpstream) String() string   { return "udp://" + u.addr }
func (u *udpUpstream) Protocol() string { return "udp" }

// --- DoT (DNS-over-TLS) ---

type dotUpstream struct {
	addr   string
	client *dns.Client
}

func newDotUpstream(addr string, timeout time.Duration, tlsCfg *tls.Config) *dotUpstream {
	// Ensure addr has a port; default to 853.
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "853")
	}

	cfg := &tls.Config{}
	if tlsCfg != nil {
		cfg = tlsCfg.Clone()
	}
	// Derive SNI from hostname if not explicitly set.
	if cfg.ServerName == "" {
		if host, _, err := net.SplitHostPort(addr); err == nil && net.ParseIP(host) == nil {
			cfg.ServerName = host
		}
	}

	return &dotUpstream{
		addr: addr,
		client: &dns.Client{
			Net:       "tcp-tls",
			TLSConfig: cfg,
			Timeout:   timeout,
		},
	}
}

func (u *dotUpstream) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, error) {
	return u.client.ExchangeContext(ctx, msg, u.addr)
}

func (u *dotUpstream) String() string   { return "tls://" + u.addr }
func (u *dotUpstream) Protocol() string { return "dot" }

// --- DoH (DNS-over-HTTPS, RFC 8484) ---

type dohUpstream struct {
	url    string
	client *http.Client
}

func newDohUpstream(rawURL string, timeout time.Duration, tlsCfg *tls.Config) *dohUpstream {
	transport := &http.Transport{}
	if tlsCfg != nil {
		transport.TLSClientConfig = tlsCfg.Clone()
	}
	return &dohUpstream{
		url: rawURL,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}
}

func (u *dohUpstream) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, error) {
	wire, err := msg.Pack()
	if err != nil {
		return nil, 0, fmt.Errorf("pack query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.url, bytes.NewReader(wire))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	start := time.Now()
	resp, err := u.client.Do(req)
	rtt := time.Since(start)
	if err != nil {
		return nil, 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, rtt, fmt.Errorf("DoH server returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, rtt, fmt.Errorf("read body: %w", err)
	}

	reply := new(dns.Msg)
	if err := reply.Unpack(body); err != nil {
		return nil, rtt, fmt.Errorf("unpack response: %w", err)
	}
	return reply, rtt, nil
}

func (u *dohUpstream) String() string   { return u.url }
func (u *dohUpstream) Protocol() string { return "doh" }
