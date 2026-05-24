package overrides

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// Record is a single local DNS override entry from configuration.
type Record struct {
	Name  string `mapstructure:"name"`
	Type  string `mapstructure:"type"`
	TTL   uint32 `mapstructure:"ttl"`
	Value string `mapstructure:"value"`
}

// Resolver answers DNS questions from a static set of local override records.
// An empty Resolver (New called with nil) answers no queries.
type Resolver struct {
	rrs map[string][]dns.RR
}

// New builds a Resolver from the provided records.
// Returns an error if any record has an unknown type or malformed value.
func New(records []Record) (*Resolver, error) {
	r := &Resolver{rrs: make(map[string][]dns.RR, len(records))}
	for _, rec := range records {
		if err := r.add(rec); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Resolver) add(rec Record) error {
	name := dns.Fqdn(rec.Name)
	typStr := strings.ToUpper(rec.Type)

	qtype, ok := dns.StringToType[typStr]
	if !ok {
		return fmt.Errorf("overrides: unknown record type %q for name %q", rec.Type, rec.Name)
	}

	// dns.NewRR parses a single resource record in zone-file format:
	// "name TTL IN type value"
	line := fmt.Sprintf("%s %d IN %s %s", name, rec.TTL, typStr, rec.Value)
	rr, err := dns.NewRR(line)
	if err != nil {
		return fmt.Errorf("overrides: invalid record %q: %w", line, err)
	}

	k := rrKey(name, qtype)
	r.rrs[k] = append(r.rrs[k], rr)
	return nil
}

// Lookup returns matching RRs for the DNS question and true if a local override exists.
func (r *Resolver) Lookup(q dns.Question) ([]dns.RR, bool) {
	rrs, ok := r.rrs[rrKey(dns.Fqdn(q.Name), q.Qtype)]
	if !ok || len(rrs) == 0 {
		return nil, false
	}
	return rrs, true
}

func rrKey(name string, qtype uint16) string {
	return name + ":" + dns.TypeToString[qtype]
}
