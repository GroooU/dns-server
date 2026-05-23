// Test client: go run ./cmd/query <name> [server] [type]
// Example:    go run ./cmd/query google.com 127.0.0.1:5353 A
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
)

func main() {
	name := "google.com"
	server := "127.0.0.1:5353"
	qtype := dns.TypeA

	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	if len(os.Args) > 2 {
		server = os.Args[2]
	}
	if len(os.Args) > 3 {
		t, ok := dns.StringToType[strings.ToUpper(os.Args[3])]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown type: %s\n", os.Args[3])
			os.Exit(1)
		}
		qtype = t
	}

	proto := "udp"
	if len(os.Args) > 4 && os.Args[4] == "tcp" {
		proto = "tcp"
	}
	c := &dns.Client{Net: proto, Timeout: 5 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.RecursionDesired = true

	fmt.Printf("querying %s %s @%s\n\n", name, dns.TypeToString[qtype], server)

	resp, rtt, err := c.Exchange(m, server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("rcode:  %s\n", dns.RcodeToString[resp.Rcode])
	fmt.Printf("rtt:    %v\n", rtt)
	fmt.Printf("answers: %d\n\n", len(resp.Answer))
	for _, rr := range resp.Answer {
		fmt.Printf("  %s\n", rr)
	}
}
