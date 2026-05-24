package dnssec

import (
	"errors"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// Mode controls how this forwarder handles DNSSEC.
type Mode int

const (
	ModeOff    Mode = iota // no DNSSEC processing
	ModeAD                 // trust AD bit from upstream validating resolver
	ModeVerify             // local RRSIG cryptographic check (partial, no chain of trust)
)

// Result is the outcome of a single validation call.
type Result int

const (
	ResultSecure        Result = iota // upstream confirmed authentic data
	ResultInsecure                    // zone is not signed, or DO not requested
	ResultBogus                       // signature present but fails verification
	ResultIndeterminate               // mode is off, or cannot determine status
)

func (r Result) String() string {
	switch r {
	case ResultSecure:
		return "secure"
	case ResultInsecure:
		return "insecure"
	case ResultBogus:
		return "bogus"
	default:
		return "indeterminate"
	}
}

// ErrInvalidMode is returned by New when the mode string is not recognised.
var ErrInvalidMode = errors.New("dnssec: invalid mode; must be one of: off, ad, verify")

// Validator holds the mode configuration for DNSSEC processing.
type Validator struct {
	mode Mode
	log  *zap.Logger
}

// New creates a Validator from the string mode read from config.
// Returns ErrInvalidMode for unrecognised mode strings.
func New(mode string, log *zap.Logger) (*Validator, error) {
	var m Mode
	switch mode {
	case "off", "":
		m = ModeOff
	case "ad":
		m = ModeAD
	case "verify":
		m = ModeVerify
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidMode, mode)
	}
	return &Validator{mode: m, log: log}, nil
}

// Mode returns the configured mode.
func (v *Validator) Mode() Mode { return v.mode }

// Enabled returns true when mode != off.
func (v *Validator) Enabled() bool { return v.mode != ModeOff }

// Validate inspects resp and returns the DNSSEC Result.
// req is the original client request; resp is the upstream response.
func (v *Validator) Validate(req, resp *dns.Msg) Result {
	if v.mode == ModeOff {
		return ResultIndeterminate
	}
	switch v.mode {
	case ModeAD:
		return v.validateAD(resp)
	case ModeVerify:
		return v.validateVerify(resp)
	default:
		return ResultIndeterminate
	}
}

// validateAD checks the AD (Authenticated Data) bit in the upstream response.
// AD=1 means the upstream validating resolver has verified the chain of trust.
// AD=0 in this mode is reported as Insecure because we cannot distinguish an
// unsigned zone from a validation failure without performing local crypto.
func (v *Validator) validateAD(resp *dns.Msg) Result {
	if resp.AuthenticatedData {
		return ResultSecure
	}
	return ResultInsecure
}

// validateVerify performs a local cryptographic RRSIG check.
// It collects DNSKEY records from resp.Extra and verifies each RRSIG in resp.Answer.
// This is a partial check — no chain of trust is built, no DS lookups are performed.
func (v *Validator) validateVerify(resp *dns.Msg) Result {
	dnskeys := extractDNSKEYs(resp.Extra)
	rrsigs := extractRRSIGs(resp.Answer)

	if len(rrsigs) == 0 {
		return ResultInsecure
	}
	if len(dnskeys) == 0 {
		v.log.Debug("dnssec verify: RRSIGs present but no DNSKEY in Additional section")
		return ResultIndeterminate
	}

	rrsets := groupRRsets(resp.Answer)

	bogusCount := 0
	secureCount := 0

	for _, rrsig := range rrsigs {
		key := rrsetKey{name: rrsig.Hdr.Name, rrtype: rrsig.TypeCovered}
		rrset, ok := rrsets[key]
		if !ok {
			continue
		}

		if !rrsig.ValidityPeriod(time.Now()) {
			bogusCount++
			v.log.Debug("dnssec verify: RRSIG expired or not yet valid",
				zap.String("name", rrsig.Hdr.Name),
				zap.Uint16("type", rrsig.TypeCovered),
			)
			continue
		}

		verified := false
		for _, dnskey := range dnskeys {
			if dnskey.KeyTag() != rrsig.KeyTag {
				continue
			}
			if err := rrsig.Verify(dnskey, rrset); err == nil {
				verified = true
				break
			}
		}
		if verified {
			secureCount++
		} else {
			bogusCount++
			v.log.Debug("dnssec verify: RRSIG verification failed",
				zap.String("name", rrsig.Hdr.Name),
				zap.Uint16("type", rrsig.TypeCovered),
			)
		}
	}

	switch {
	case bogusCount > 0:
		return ResultBogus
	case secureCount > 0:
		return ResultSecure
	default:
		return ResultInsecure
	}
}

// StripDNSSECRRs removes RRSIG, NSEC, and NSEC3 records from msg in-place.
// Call before sending to a client that did not set the DO bit.
func StripDNSSECRRs(msg *dns.Msg) {
	msg.Answer = filterDNSSEC(msg.Answer)
	msg.Ns = filterDNSSEC(msg.Ns)
}

func filterDNSSEC(rrs []dns.RR) []dns.RR {
	out := rrs[:0]
	for _, rr := range rrs {
		switch rr.Header().Rrtype {
		case dns.TypeRRSIG, dns.TypeNSEC, dns.TypeNSEC3:
			// drop
		default:
			out = append(out, rr)
		}
	}
	return out
}

type rrsetKey struct {
	name   string
	rrtype uint16
}

func groupRRsets(rrs []dns.RR) map[rrsetKey][]dns.RR {
	m := make(map[rrsetKey][]dns.RR)
	for _, rr := range rrs {
		if rr.Header().Rrtype == dns.TypeRRSIG {
			continue
		}
		k := rrsetKey{name: rr.Header().Name, rrtype: rr.Header().Rrtype}
		m[k] = append(m[k], rr)
	}
	return m
}

func extractRRSIGs(rrs []dns.RR) []*dns.RRSIG {
	var out []*dns.RRSIG
	for _, rr := range rrs {
		if sig, ok := rr.(*dns.RRSIG); ok {
			out = append(out, sig)
		}
	}
	return out
}

func extractDNSKEYs(rrs []dns.RR) []*dns.DNSKEY {
	var out []*dns.DNSKEY
	for _, rr := range rrs {
		if key, ok := rr.(*dns.DNSKEY); ok {
			out = append(out, key)
		}
	}
	return out
}
