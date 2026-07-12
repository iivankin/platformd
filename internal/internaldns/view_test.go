package internaldns

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type forwarderFunc func(context.Context, []byte) ([]byte, error)

func (function forwarderFunc) Resolve(ctx context.Context, packet []byte) ([]byte, error) {
	return function(ctx, packet)
}

func TestViewKeepsInternalRecordsInsideProject(t *testing.T) {
	forwarder := forwarderFunc(func(_ context.Context, packet []byte) ([]byte, error) {
		var parser dnsmessage.Parser
		header, _ := parser.Start(packet)
		question, _ := parser.Question()
		address := [4]byte{1, 1, 1, 1}
		return responseFor(header, question, dnsmessage.RCodeSuccess, &address)
	})
	alpha := mustView(t, map[string]netip.Addr{"api.alpha.internal": netip.MustParseAddr("10.80.1.2")}, forwarder)
	beta := mustView(t, map[string]netip.Addr{"api.beta.internal": netip.MustParseAddr("10.80.2.2")}, forwarder)

	assertViewResult(t, alpha, dnsQuery(t, 1, "api.alpha.internal.", dnsmessage.TypeA), dnsmessage.RCodeSuccess, "10.80.1.2")
	assertViewResult(t, alpha, dnsQuery(t, 2, "api.alpha.internal.", dnsmessage.TypeAAAA), dnsmessage.RCodeSuccess, "")
	assertViewResult(t, alpha, dnsQuery(t, 3, "api.beta.internal.", dnsmessage.TypeA), dnsmessage.RCodeNameError, "")
	assertViewResult(t, beta, dnsQuery(t, 4, "api.alpha.internal.", dnsmessage.TypeA), dnsmessage.RCodeNameError, "")
	assertViewResult(t, alpha, dnsQuery(t, 5, "example.com.", dnsmessage.TypeA), dnsmessage.RCodeSuccess, "1.1.1.1")
}

func TestServerAnswersUDPAndTCP(t *testing.T) {
	view := mustView(t, map[string]netip.Addr{"api.alpha.internal": netip.MustParseAddr("127.0.0.7")}, forwarderFunc(func(context.Context, []byte) ([]byte, error) {
		t.Fatal("internal query was forwarded")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := Start(ctx, ServerConfig{Address: netip.MustParseAddr("127.0.0.1"), View: view})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	query := dnsQuery(t, 10, "api.alpha.internal.", dnsmessage.TypeA)

	udp, err := net.DialTimeout("udp4", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	if err := udp.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := udp.Write(query); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, maxDNSMessageBytes)
	length, err := udp.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	assertDNSResult(t, buffer[:length], dnsmessage.RCodeSuccess, "127.0.0.7")

	tcp, err := net.DialTimeout("tcp4", server.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(query)))
	if _, err := tcp.Write(append(size[:], query...)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(tcp, size[:]); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(tcp, response); err != nil {
		t.Fatal(err)
	}
	assertDNSResult(t, response, dnsmessage.RCodeSuccess, "127.0.0.7")
}

func TestForwardCacheReusesResponseWithCurrentID(t *testing.T) {
	upstream, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	var requests atomic.Int32
	go func() {
		buffer := make([]byte, maxDNSMessageBytes)
		for {
			length, address, readErr := upstream.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			requests.Add(1)
			packet := append([]byte(nil), buffer[:length]...)
			var parser dnsmessage.Parser
			header, _ := parser.Start(packet)
			question, _ := parser.Question()
			answer := [4]byte{9, 9, 9, 9}
			response, _ := responseFor(header, question, dnsmessage.RCodeSuccess, &answer)
			_, _ = upstream.WriteTo(response, address)
		}
	}()
	upstreamAddress := upstream.LocalAddr().(*net.UDPAddr).AddrPort()
	forwarder, err := NewForwardCache([]netip.AddrPort{upstreamAddress}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := forwarder.Resolve(context.Background(), dnsQuery(t, 20, "example.com.", dnsmessage.TypeA))
	if err != nil {
		t.Fatal(err)
	}
	second, err := forwarder.Resolve(context.Background(), dnsQuery(t, 21, "example.com.", dnsmessage.TypeA))
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || binary.BigEndian.Uint16(first[:2]) != 20 || binary.BigEndian.Uint16(second[:2]) != 21 {
		t.Fatalf("cache requests=%d firstID=%d secondID=%d", requests.Load(), binary.BigEndian.Uint16(first[:2]), binary.BigEndian.Uint16(second[:2]))
	}
}

func mustView(t *testing.T, records map[string]netip.Addr, forwarder Forwarder) *View {
	t.Helper()
	zone, err := NewZone(records)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewView(zone, forwarder)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func dnsQuery(t *testing.T, id uint16, name string, recordType dnsmessage.Type) []byte {
	t.Helper()
	message := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: recordType, Class: dnsmessage.ClassINET}},
	}
	packet, err := message.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

func assertViewResult(t *testing.T, view *View, query []byte, code dnsmessage.RCode, expectedAddress string) {
	t.Helper()
	packet, err := view.Resolve(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	assertDNSResult(t, packet, code, expectedAddress)
}

func assertDNSResult(t *testing.T, packet []byte, code dnsmessage.RCode, expectedAddress string) {
	t.Helper()
	var parser dnsmessage.Parser
	header, err := parser.Start(packet)
	if err != nil {
		t.Fatal(err)
	}
	if header.RCode != code || !header.Response {
		t.Fatalf("unexpected DNS header: %+v", header)
	}
	if err := parser.SkipAllQuestions(); err != nil {
		t.Fatal(err)
	}
	answer, err := parser.AnswerHeader()
	if expectedAddress == "" {
		if err != dnsmessage.ErrSectionDone {
			t.Fatalf("unexpected DNS answer %+v, %v", answer, err)
		}
		return
	}
	if err != nil || answer.Type != dnsmessage.TypeA || answer.TTL != internalTTL {
		t.Fatalf("unexpected DNS answer header %+v, %v", answer, err)
	}
	resource, err := parser.AResource()
	if err != nil || netip.AddrFrom4(resource.A).String() != expectedAddress {
		t.Fatalf("unexpected DNS A record %+v, %v", resource, err)
	}
}
