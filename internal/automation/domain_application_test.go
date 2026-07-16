package automation

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type domainRepositoryStub struct {
	attachCalls int
	attach      state.AttachServiceDomainInput
	detach      state.DetachServiceDomainInput
}

func (*domainRepositoryStub) ServiceDomains(context.Context, string, string) ([]state.ServiceDomain, error) {
	return []state.ServiceDomain{{Hostname: "app.example.com", ServiceID: "service"}}, nil
}

func (repository *domainRepositoryStub) AttachServiceDomain(_ context.Context, input state.AttachServiceDomainInput) (state.ServiceDomain, error) {
	repository.attachCalls++
	repository.attach = input
	return state.ServiceDomain{Hostname: input.Hostname, ProjectID: input.ProjectID, ServiceID: input.ServiceID, TargetPort: input.TargetPort}, nil
}

func (repository *domainRepositoryStub) DetachServiceDomain(_ context.Context, input state.DetachServiceDomainInput) error {
	repository.detach = input
	return nil
}

func TestDomainApplicationEnforcesBoundaryAndCreatesTokenAuditInputs(t *testing.T) {
	repository := &domainRepositoryStub{}
	application, err := NewDomainApplication(repository, bytes.NewReader(make([]byte, 128)), func() time.Time {
		return time.UnixMilli(1_700_000_000_000)
	})
	if err != nil {
		t.Fatal(err)
	}
	bound := "project"
	other := "other"
	if _, err := application.List(context.Background(), Identity{TokenID: "read", Role: "read", ProjectID: &bound}, other, "service"); !errors.Is(err, ErrProjectBoundary) {
		t.Fatalf("cross-project read error = %v", err)
	}
	input := AttachDomainInput{ProjectID: bound, ServiceID: "service", Hostname: "app.example.com", TargetPort: 8080, Move: true}
	if _, err := application.Attach(context.Background(), Identity{TokenID: "read", Role: "read", ProjectID: &bound}, input); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("read attach error = %v", err)
	}
	result, err := application.Attach(context.Background(), Identity{TokenID: "admin", Role: "admin", ProjectID: &bound}, input)
	if err != nil {
		t.Fatal(err)
	}
	if repository.attachCalls != 1 || repository.attach.ActorKind != "token" || repository.attach.ActorID != "admin" || repository.attach.TargetPort != 8080 || !repository.attach.Move || result.RequestID == "" {
		t.Fatalf("attach = calls=%d input=%+v result=%+v", repository.attachCalls, repository.attach, result)
	}
	if _, err := application.Detach(context.Background(), Identity{TokenID: "admin", Role: "admin", ProjectID: &bound}, DetachDomainInput{
		ProjectID: bound, ServiceID: "service", Hostname: "app.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if repository.detach.ActorKind != "token" || repository.detach.ActorID != "admin" || repository.detach.AuditEventID == "" {
		t.Fatalf("detach input = %+v", repository.detach)
	}
}
