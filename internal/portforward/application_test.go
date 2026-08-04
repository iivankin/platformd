package portforward

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

type resourceRepositoryStub struct {
	kind        string
	portForward *serviceconfig.PortForward
	projectErr  error
	resourceErr error
}

func (repository resourceRepositoryStub) ResolveProject(_ context.Context, name string) (ResolvedProject, error) {
	return ResolvedProject{ID: "project", Name: name}, repository.projectErr
}

func (repository resourceRepositoryStub) ResolveResource(_ context.Context, _ string, name string) (ResolvedResource, error) {
	kind := repository.kind
	if kind == "" {
		kind = "postgres"
	}
	return ResolvedResource{
		ID: "resource-id", Kind: kind, Name: name, PortForward: repository.portForward,
	}, repository.resourceErr
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
	projectID := "project"
	identity := automation.Identity{TokenID: "admin-token", Role: "admin", ProjectID: &projectID}
	grant, err := application.Create(context.Background(), identity, CreateInput{
		Project: "shop", Resource: "database", Port: 5432,
		LifetimeSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.ID != "ticket-id" || grant.Project != "shop" || grant.Resource != "database" || grant.ResourceKind != "postgres" ||
		!strings.HasPrefix(grant.Ticket, "pft_") || audit.record.ActorTokenID != "admin-token" ||
		audit.record.ProjectID != "project" || audit.record.ResourceID != "resource-id" || audit.record.Port != 5432 {
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
	input := CreateInput{Project: "shop", Resource: "cache", Port: 6379}
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

type oidcVerifierStub struct {
	identity OIDCIdentity
	err      error
	calls    int
}

func (verifier *oidcVerifierStub) Verify(context.Context, string, string, string, []string) (OIDCIdentity, error) {
	verifier.calls++
	return verifier.identity, verifier.err
}

func TestCreateOIDCAllowsServicePostgresRedisAndObjectStore(t *testing.T) {
	for _, kind := range []string{"service", "postgres", "redis", "object_store"} {
		t.Run(kind, func(t *testing.T) {
			port := 8080
			address := "10.42.0.8:8080"
			if kind == "postgres" {
				port = 5432
				address = "10.42.0.8:5432"
			}
			if kind == "redis" {
				port = 6379
				address = "10.42.0.8:6379"
			}
			if kind == "object_store" {
				port = 9000
				address = "10.42.0.1:9000"
			}
			resolver := &resolverStub{address: address}
			audit := &auditStub{}
			oidc := &oidcVerifierStub{identity: OIDCIdentity{Repository: "acme/app", RunID: "123"}}
			application, err := New(Config{
				Repository: resourceRepositoryStub{
					kind: kind,
					portForward: &serviceconfig.PortForward{
						Repository: "acme/app",
						Workflows:  []string{"ci.yml"},
					},
				},
				Resolver: resolver, Audit: audit, OIDC: oidc,
				NewID: func() (string, error) { return "ticket-id", nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			grant, err := application.CreateOIDC(context.Background(), CreateInput{
				Project: "shop", Resource: "target", Port: port, LifetimeSeconds: 60,
			}, "oidc-token", "https://admin.example.com/public/api/v1/projects/shop/resources/target/port-forwards")
			if err != nil {
				t.Fatal(err)
			}
			if grant.ResourceKind != kind || audit.record.ActorTokenID != "oidc:123" || oidc.calls != 1 {
				t.Fatalf("grant/audit = %+v / %+v / calls=%d", grant, audit.record, oidc.calls)
			}
		})
	}
}

func TestCreateOIDCRejectsMissingAllowlist(t *testing.T) {
	application, err := New(Config{
		Repository: resourceRepositoryStub{kind: "postgres"},
		Resolver:   &resolverStub{address: "10.42.0.8:5432"},
		Audit:      &auditStub{},
		OIDC:       &oidcVerifierStub{identity: OIDCIdentity{Repository: "acme/app", RunID: "123"}},
		NewID:      func() (string, error) { return "ticket-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.CreateOIDC(context.Background(), CreateInput{
		Project: "shop", Resource: "database", Port: 5432,
	}, "oidc-token", "https://admin.example.com/audience"); !errors.Is(err, ErrOIDCUnauthorized) {
		t.Fatalf("missing allowlist error = %v", err)
	}
}

func TestCreateAllowsObjectStore(t *testing.T) {
	resolver := &resolverStub{address: "10.42.0.1:9000"}
	audit := &auditStub{}
	application, err := New(Config{
		Repository: resourceRepositoryStub{kind: "object_store"},
		Resolver:   resolver, Audit: audit,
		NewID: func() (string, error) { return "ticket-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := application.Create(context.Background(), automation.Identity{TokenID: "admin", Role: "admin"}, CreateInput{
		Project: "shop", Resource: "assets", Port: 9000, LifetimeSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.ResourceKind != "object_store" {
		t.Fatalf("resource kind = %s", grant.ResourceKind)
	}
}
