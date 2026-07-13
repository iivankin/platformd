package automation

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type projectCreatorStub struct {
	calls int
	input state.CreateProjectByToken
}

func (creator *projectCreatorStub) CreateProjectByToken(_ context.Context, input state.CreateProjectByToken) (state.ProjectSummary, error) {
	creator.calls++
	creator.input = input
	return state.ProjectSummary{ID: input.ID, Name: input.Name, CreatedAtMillis: input.CreatedAtMillis}, nil
}

func TestProjectApplicationRequiresUnboundAdminAndBuildsTokenAudit(t *testing.T) {
	creator := &projectCreatorStub{}
	application, err := NewProjectApplication(creator, bytes.NewReader(make([]byte, 96)), func() time.Time {
		return time.UnixMilli(1_700_000_000_000)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Create(context.Background(), Identity{TokenID: "read", Role: "read"}, "shop"); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("read token error = %v", err)
	}
	bound := "existing"
	if _, err := application.Create(context.Background(), Identity{TokenID: "admin", Role: "admin", ProjectID: &bound}, "shop"); !errors.Is(err, ErrProjectBoundary) {
		t.Fatalf("bound admin error = %v", err)
	}
	if creator.calls != 0 {
		t.Fatalf("creator called before authorization: %d", creator.calls)
	}
	result, err := application.Create(context.Background(), Identity{TokenID: "admin", Role: "admin"}, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if creator.calls != 1 || creator.input.ActorTokenID != "admin" || creator.input.ID == "" || creator.input.AuditEventID == "" || result.RequestID == "" {
		t.Fatalf("project mutation = calls=%d input=%+v result=%+v", creator.calls, creator.input, result)
	}
}
