package dnssec

import (
	"crypto"
	"testing"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

func nopLog() *zap.Logger { return zap.NewNop() }

func mustNew(t *testing.T, mode string) *Validator {
	t.Helper()
	v, err := New(mode, nopLog())
	if err != nil {
		t.Fatalf("New(%q): %v", mode, err)
	}
	return v
}

// makeResp builds a minimal DNS response.
func makeResp() *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(&dns.Msg{})
	m.Rcode = dns.RcodeSuccess
	return m
}

// makeRRSIG returns a stub RRSIG (invalid signature) covering the given type.
func makeRRSIG(name string, covered uint16, keytag uint16, exp time.Time) *dns.RRSIG {
	return &dns.RRSIG{
		Hdr:         dns.RR_Header{Name: name, Rrtype: dns.TypeRRSIG, Class: dns.ClassINET, Ttl: 300},
		TypeCovered: covered,
		Algorithm:   dns.ECDSAP256SHA256,
		Labels:      1,
		Expiration:  uint32(exp.Unix()),
		Inception:   uint32(time.Now().Add(-time.Hour).Unix()),
		KeyTag:      keytag,
		SignerName:  name,
		Signature:   "AAAA", // deliberately invalid (base64-encoded zeros)
	}
}

func makeARecord(name string) *dns.A {
	return &dns.A{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   []byte{1, 2, 3, 4},
	}
}

// generateKeyAndSign creates a real DNSKEY and signs rrset with it.
// Returns (dnskey, rrsig). Fails the test on any error.
func generateKeyAndSign(t *testing.T, name string, rrset []dns.RR) (*dns.DNSKEY, *dns.RRSIG) {
	t.Helper()

	dnskey := &dns.DNSKEY{
		Hdr:       dns.RR_Header{Name: name, Rrtype: dns.TypeDNSKEY, Class: dns.ClassINET, Ttl: 300},
		Flags:     dns.ZONE | dns.SEP,
		Protocol:  3,
		Algorithm: dns.ECDSAP256SHA256,
	}
	privKey, err := dnskey.Generate(256)
	if err != nil {
		t.Fatalf("DNSKEY.Generate: %v", err)
	}

	rrsig := &dns.RRSIG{
		Hdr:         dns.RR_Header{Name: name, Rrtype: dns.TypeRRSIG, Class: dns.ClassINET, Ttl: 300},
		TypeCovered: rrset[0].Header().Rrtype,
		Algorithm:   dns.ECDSAP256SHA256,
		Labels:      1,
		OrigTtl:     300,
		Expiration:  uint32(time.Now().Add(24 * time.Hour).Unix()),
		Inception:   uint32(time.Now().Add(-time.Hour).Unix()),
		KeyTag:      dnskey.KeyTag(),
		SignerName:  name,
	}
	if err := rrsig.Sign(privKey.(crypto.Signer), rrset); err != nil {
		t.Fatalf("RRSIG.Sign: %v", err)
	}
	return dnskey, rrsig
}

// --- Mode tests ---

func TestNewInvalidMode(t *testing.T) {
	_, err := New("foobar", nopLog())
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestOffMode(t *testing.T) {
	v := mustNew(t, "off")
	resp := makeResp()
	resp.AuthenticatedData = true
	if got := v.Validate(nil, resp); got != ResultIndeterminate {
		t.Errorf("off mode: got %v, want Indeterminate", got)
	}
}

// --- AD mode tests ---

func TestADModeSecure(t *testing.T) {
	v := mustNew(t, "ad")
	resp := makeResp()
	resp.AuthenticatedData = true
	if got := v.Validate(nil, resp); got != ResultSecure {
		t.Errorf("ad secure: got %v, want Secure", got)
	}
}

func TestADModeInsecure(t *testing.T) {
	v := mustNew(t, "ad")
	resp := makeResp()
	resp.AuthenticatedData = false
	if got := v.Validate(nil, resp); got != ResultInsecure {
		t.Errorf("ad insecure: got %v, want Insecure", got)
	}
}

// --- Verify mode tests ---

func TestVerifyModeNoRRSIG(t *testing.T) {
	v := mustNew(t, "verify")
	resp := makeResp()
	resp.Answer = []dns.RR{makeARecord("example.com.")}
	if got := v.Validate(nil, resp); got != ResultInsecure {
		t.Errorf("verify no-rrsig: got %v, want Insecure", got)
	}
}

func TestVerifyModeNoDNSKEY(t *testing.T) {
	v := mustNew(t, "verify")
	resp := makeResp()
	resp.Answer = []dns.RR{
		makeARecord("example.com."),
		makeRRSIG("example.com.", dns.TypeA, 1234, time.Now().Add(time.Hour)),
	}
	// No DNSKEY in Extra → Indeterminate
	if got := v.Validate(nil, resp); got != ResultIndeterminate {
		t.Errorf("verify no-dnskey: got %v, want Indeterminate", got)
	}
}

func TestVerifyModeBadSig(t *testing.T) {
	v := mustNew(t, "verify")

	name := "example.com."
	aRR := makeARecord(name)
	// Generate a real DNSKEY so we have a valid key to verify against,
	// but the RRSIG has a deliberately invalid (zero) signature.
	dnskey := &dns.DNSKEY{
		Hdr:       dns.RR_Header{Name: name, Rrtype: dns.TypeDNSKEY, Class: dns.ClassINET, Ttl: 300},
		Flags:     dns.ZONE | dns.SEP,
		Protocol:  3,
		Algorithm: dns.ECDSAP256SHA256,
	}
	_, err := dnskey.Generate(256)
	if err != nil {
		t.Fatalf("DNSKEY.Generate: %v", err)
	}

	badRRSIG := makeRRSIG(name, dns.TypeA, dnskey.KeyTag(), time.Now().Add(time.Hour))

	resp := makeResp()
	resp.Answer = []dns.RR{aRR, badRRSIG}
	resp.Extra = []dns.RR{dnskey}

	if got := v.Validate(nil, resp); got != ResultBogus {
		t.Errorf("verify bad-sig: got %v, want Bogus", got)
	}
}

func TestVerifyModeValidSig(t *testing.T) {
	v := mustNew(t, "verify")

	name := "example.com."
	aRR := makeARecord(name)
	rrset := []dns.RR{aRR}

	dnskey, rrsig := generateKeyAndSign(t, name, rrset)

	resp := makeResp()
	resp.Answer = []dns.RR{aRR, rrsig}
	resp.Extra = []dns.RR{dnskey}

	if got := v.Validate(nil, resp); got != ResultSecure {
		t.Errorf("verify valid-sig: got %v, want Secure", got)
	}
}

func TestVerifyModeExpiredSig(t *testing.T) {
	v := mustNew(t, "verify")

	name := "example.com."
	aRR := makeARecord(name)
	rrset := []dns.RR{aRR}

	dnskey, rrsig := generateKeyAndSign(t, name, rrset)
	// Backdating expiration to the past makes ValidityPeriod return false.
	rrsig.Expiration = uint32(time.Now().Add(-time.Hour).Unix())
	rrsig.Inception = uint32(time.Now().Add(-2 * time.Hour).Unix())

	resp := makeResp()
	resp.Answer = []dns.RR{aRR, rrsig}
	resp.Extra = []dns.RR{dnskey}

	if got := v.Validate(nil, resp); got != ResultBogus {
		t.Errorf("verify expired-sig: got %v, want Bogus", got)
	}
}

// --- StripDNSSECRRs tests ---

func TestStripDNSSECRRs(t *testing.T) {
	name := "example.com."
	aRR := makeARecord(name)
	rrsig := makeRRSIG(name, dns.TypeA, 1, time.Now().Add(time.Hour))
	nsec := &dns.NSEC{
		Hdr:        dns.RR_Header{Name: name, Rrtype: dns.TypeNSEC, Class: dns.ClassINET, Ttl: 300},
		NextDomain: "z.example.com.",
		TypeBitMap: []uint16{dns.TypeA, dns.TypeNSEC},
	}

	msg := makeResp()
	msg.Answer = []dns.RR{aRR, rrsig, nsec}

	StripDNSSECRRs(msg)

	if len(msg.Answer) != 1 {
		t.Fatalf("after strip: got %d Answer RRs, want 1", len(msg.Answer))
	}
	if msg.Answer[0].Header().Rrtype != dns.TypeA {
		t.Errorf("after strip: remaining RR type = %v, want A", msg.Answer[0].Header().Rrtype)
	}
}
