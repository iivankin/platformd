package internaldns

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

const internalTTL = 5

type Forwarder interface {
	Resolve(context.Context, []byte) ([]byte, error)
}

type View struct {
	zone      *Zone
	forwarder Forwarder
}

func NewView(zone *Zone, forwarder Forwarder) (*View, error) {
	if zone == nil || forwarder == nil {
		return nil, errors.New("internal DNS view requires a zone and forwarder")
	}
	return &View{zone: zone, forwarder: forwarder}, nil
}

func (view *View) Resolve(ctx context.Context, packet []byte) ([]byte, error) {
	if len(packet) < 12 || len(packet) > maxDNSMessageBytes {
		return nil, errors.New("DNS query size is outside supported bounds")
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(packet)
	if err != nil {
		return nil, fmt.Errorf("parse DNS query header: %w", err)
	}
	questions, err := parser.AllQuestions()
	if err != nil || len(questions) != 1 || header.Response || header.OpCode != 0 {
		return responseFor(header, dnsmessage.Question{}, dnsmessage.RCodeFormatError, nil)
	}
	question := questions[0]
	name := strings.ToLower(question.Name.String())
	if !strings.HasSuffix(name, internalSuffix) && name != "internal." {
		response, err := view.forwarder.Resolve(ctx, packet)
		if err == nil {
			return response, nil
		}
		return responseFor(header, question, dnsmessage.RCodeServerFailure, nil)
	}
	if question.Class != dnsmessage.ClassINET {
		return responseFor(header, question, dnsmessage.RCodeRefused, nil)
	}
	address, found := view.zone.lookup(name)
	if !found {
		return responseFor(header, question, dnsmessage.RCodeNameError, nil)
	}
	if question.Type != dnsmessage.TypeA && question.Type != dnsmessage.TypeALL {
		return responseFor(header, question, dnsmessage.RCodeSuccess, nil)
	}
	return responseFor(header, question, dnsmessage.RCodeSuccess, &address)
}

func responseFor(query dnsmessage.Header, question dnsmessage.Question, code dnsmessage.RCode, address *[4]byte) ([]byte, error) {
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: query.ID, Response: true, OpCode: query.OpCode,
		Authoritative: true, RecursionDesired: query.RecursionDesired,
		RCode: code,
	})
	builder.EnableCompression()
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if question.Name.Length != 0 {
		if err := builder.Question(question); err != nil {
			return nil, err
		}
	}
	if address != nil {
		if err := builder.StartAnswers(); err != nil {
			return nil, err
		}
		if err := builder.AResource(dnsmessage.ResourceHeader{
			Name: question.Name, Type: dnsmessage.TypeA,
			Class: dnsmessage.ClassINET, TTL: internalTTL,
		}, dnsmessage.AResource{A: *address}); err != nil {
			return nil, err
		}
	}
	return builder.Finish()
}
