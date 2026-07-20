package portforward

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/automation"
)

type resourceRepositoryStub struct{ err error }

func (repository resourceRepositoryStub) Resource(context.Context, string, string, string) error {
	return repository.err
}

type resolverStub struct {
	address string
	err     error
	calls   int
}

func (resolver *resolverStub) ResolveResourceAddress(string, string, string, int) (string, error) {
	resolver.calls++
	return resolver.address, resolver.err
}

type auditStub struct {
	record AuditRecord
	err    error
}

func (audit *auditStub) RecordPortForwardTicket(_ context.Context, record AuditRecord) error {
	audit.record = record
	return audit.err
}

func TestTicketLifecycleAndConnectionLimit(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	resolver := &resolverStub{address: "10.42.0.8:5432"}
	audit := &auditStub{}
	application, err := New(Config{
		Repository: resourceRepositoryStub{}, Resolver: resolver, Audit: audit,
		Now: func() time.Time { return now }, NewID: func() (string, error) { return "ticket-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := automation.Identity{TokenID: "admin-token", Role: "admin"}
	grant, err := application.Create(context.Background(), identity, CreateInput{
		ProjectID: "project", ResourceKind: "postgres", ResourceID: "database", Port: 5432,
		LifetimeSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.ID != "ticket-id" || !strings.HasPrefix(grant.Ticket, "pft_") ||
		audit.record.ActorTokenID != "admin-token" || audit.record.Port != 5432 {
		t.Fatalf("grant/audit = %+v / %+v", grant, audit.record)
	}

	resolver.address = "10.42.0.9:5432"
	sessions := make([]*Session, 0, MaximumConnections)
	for index := 0; index < MaximumConnections; index++ {
		session, acquireErr := application.Acquire(grant.Ticket)
		if acquireErr != nil {
			t.Fatalf("acquire %d: %v", index, acquireErr)
		}
		if session.Target != resolver.address {
			t.Fatalf("resolved target = %s", session.Target)
		}
		sessions = append(sessions, session)
	}
	if _, err := application.Acquire(grant.Ticket); !errors.Is(err, ErrConnectionLimit) {
		t.Fatalf("connection limit error = %v", err)
	}
	sessions[0].Release()
	if session, err := application.Acquire(grant.Ticket); err != nil {
		t.Fatal(err)
	} else {
		session.Release()
	}
	for _, session := range sessions[1:] {
		session.Release()
	}

	now = now.Add(time.Minute)
	if _, err := application.Acquire(grant.Ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("expired ticket error = %v", err)
	}
}

func TestTicketCreationRequiresBoundAdminAndAudit(t *testing.T) {
	resolver := &resolverStub{address: "10.42.0.8:6379"}
	audit := &auditStub{err: errors.New("audit unavailable")}
	application, err := New(Config{
		Repository: resourceRepositoryStub{}, Resolver: resolver, Audit: audit,
		NewID: func() (string, error) { return "ticket-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateInput{ProjectID: "project", ResourceKind: "redis", ResourceID: "cache", Port: 6379}
	if _, err := application.Create(context.Background(), automation.Identity{Role: "read"}, input); !errors.Is(err, automation.ErrAdminRequired) {
		t.Fatalf("read identity error = %v", err)
	}
	otherProject := "other"
	if _, err := application.Create(context.Background(), automation.Identity{Role: "admin", ProjectID: &otherProject}, input); !errors.Is(err, automation.ErrProjectBoundary) {
		t.Fatalf("project boundary error = %v", err)
	}
	grant, err := application.Create(context.Background(), automation.Identity{TokenID: "admin", Role: "admin"}, input)
	if err == nil || grant.Ticket != "" {
		t.Fatalf("audit failure created grant: %+v / %v", grant, err)
	}
}
