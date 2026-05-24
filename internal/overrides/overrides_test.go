package overrides_test

import (
	"testing"

	"dns-forwarder/internal/overrides"

	"github.com/miekg/dns"
)

func newResolver(t *testing.T, records []overrides.Record) *overrides.Resolver {
	t.Helper()
	res, err := overrides.New(records)
	if err != nil {
		t.Fatalf("overrides.New: %v", err)
	}
	return res
}

func question(name string, qtype uint16) dns.Question {
	return dns.Question{Name: dns.Fqdn(name), Qtype: qtype, Qclass: dns.ClassINET}
}

func TestLookup_A(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "test.local.", Type: "A", TTL: 300, Value: "192.168.1.1"},
	})

	rrs, ok := res.Lookup(question("test.local.", dns.TypeA))
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if len(rrs) != 1 {
		t.Fatalf("expected 1 RR, got %d", len(rrs))
	}
	a, ok := rrs[0].(*dns.A)
	if !ok {
		t.Fatalf("expected *dns.A, got %T", rrs[0])
	}
	if a.A.String() != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", a.A)
	}
	if a.Hdr.Ttl != 300 {
		t.Errorf("expected TTL 300, got %d", a.Hdr.Ttl)
	}
}

func TestLookup_AAAA(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "v6.local.", Type: "AAAA", TTL: 60, Value: "fd00::1"},
	})

	rrs, ok := res.Lookup(question("v6.local.", dns.TypeAAAA))
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if _, ok := rrs[0].(*dns.AAAA); !ok {
		t.Fatalf("expected *dns.AAAA, got %T", rrs[0])
	}
}

func TestLookup_Miss(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "test.local.", Type: "A", TTL: 300, Value: "192.168.1.1"},
	})

	if _, ok := res.Lookup(question("other.local.", dns.TypeA)); ok {
		t.Fatal("expected miss for unknown name, got hit")
	}
}

func TestLookup_TypeMismatch(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "test.local.", Type: "A", TTL: 300, Value: "192.168.1.1"},
	})

	if _, ok := res.Lookup(question("test.local.", dns.TypeAAAA)); ok {
		t.Fatal("expected miss for wrong type, got hit")
	}
}

func TestLookup_MultipleRecords(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "multi.local.", Type: "A", TTL: 300, Value: "192.168.1.1"},
		{Name: "multi.local.", Type: "A", TTL: 300, Value: "192.168.1.2"},
	})

	rrs, ok := res.Lookup(question("multi.local.", dns.TypeA))
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if len(rrs) != 2 {
		t.Fatalf("expected 2 RRs, got %d", len(rrs))
	}
}

func TestLookup_NameNormalization(t *testing.T) {
	// Record without trailing dot must match query with trailing dot (FQDN).
	res := newResolver(t, []overrides.Record{
		{Name: "test.local", Type: "A", TTL: 300, Value: "10.0.0.1"},
	})

	rrs, ok := res.Lookup(question("test.local.", dns.TypeA))
	if !ok {
		t.Fatal("expected hit after FQDN normalization, got miss")
	}
	if len(rrs) != 1 {
		t.Fatalf("expected 1 RR, got %d", len(rrs))
	}
}

func TestLookup_CNAME(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "alias.local.", Type: "CNAME", TTL: 300, Value: "target.local."},
	})

	rrs, ok := res.Lookup(question("alias.local.", dns.TypeCNAME))
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	cname, ok := rrs[0].(*dns.CNAME)
	if !ok {
		t.Fatalf("expected *dns.CNAME, got %T", rrs[0])
	}
	if cname.Target != "target.local." {
		t.Errorf("expected target.local., got %s", cname.Target)
	}
}

func TestLookup_TXT(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "txt.local.", Type: "TXT", TTL: 300, Value: `"v=spf1 -all"`},
	})

	rrs, ok := res.Lookup(question("txt.local.", dns.TypeTXT))
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if _, ok := rrs[0].(*dns.TXT); !ok {
		t.Fatalf("expected *dns.TXT, got %T", rrs[0])
	}
}

func TestLookup_MX(t *testing.T) {
	res := newResolver(t, []overrides.Record{
		{Name: "mail.local.", Type: "MX", TTL: 300, Value: "10 smtp.local."},
	})

	rrs, ok := res.Lookup(question("mail.local.", dns.TypeMX))
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	mx, ok := rrs[0].(*dns.MX)
	if !ok {
		t.Fatalf("expected *dns.MX, got %T", rrs[0])
	}
	if mx.Preference != 10 {
		t.Errorf("expected preference 10, got %d", mx.Preference)
	}
}

func TestNew_InvalidType(t *testing.T) {
	_, err := overrides.New([]overrides.Record{
		{Name: "test.local.", Type: "BOGUS", TTL: 300, Value: "192.168.1.1"},
	})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestNew_InvalidValue(t *testing.T) {
	_, err := overrides.New([]overrides.Record{
		{Name: "test.local.", Type: "A", TTL: 300, Value: "not-an-ip"},
	})
	if err == nil {
		t.Fatal("expected error for invalid A record value, got nil")
	}
}

func TestNew_Empty(t *testing.T) {
	res, err := overrides.New(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil records: %v", err)
	}
	if _, ok := res.Lookup(question("anything.local.", dns.TypeA)); ok {
		t.Fatal("empty resolver should always return miss")
	}
}
