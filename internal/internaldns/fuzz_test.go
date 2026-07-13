package internaldns

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func FuzzDNSInputs(f *testing.F) {
	zone, err := NewZone(map[string]netip.Addr{
		"api.project.internal": netip.MustParseAddr("10.80.1.2"),
	})
	if err != nil {
		f.Fatal(err)
	}
	view, err := NewView(zone, forwarderFunc(func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("upstream unavailable")
	}))
	if err != nil {
		f.Fatal(err)
	}
	message := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 1, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name: dnsmessage.MustNewName("api.project.internal."),
			Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
		}},
	}
	valid, err := message.Pack()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte{0, 1, 2})

	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > maxDNSMessageBytes+1 {
			t.Skip()
		}
		response, err := view.Resolve(context.Background(), packet)
		if err != nil {
			return
		}
		if len(response) == 0 || len(response) > maxDNSMessageBytes {
			t.Fatalf("DNS response size = %d", len(response))
		}
		var parser dnsmessage.Parser
		header, parseErr := parser.Start(response)
		if parseErr != nil || !header.Response {
			t.Fatalf("invalid DNS response header: %+v, %v", header, parseErr)
		}
	})
}
