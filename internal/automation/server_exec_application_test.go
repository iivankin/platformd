package automation

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/rootexec"
	"github.com/iivankin/platformd/internal/state"
)

type serverExecutor struct {
	calls int
}

func (executor *serverExecutor) Execute(context.Context, rootexec.Request) (rootexec.Result, error) {
	executor.calls++
	return rootexec.Result{ExitCode: 0, StartedAt: 10, FinishedAt: 20, DurationMillis: 10}, nil
}

type serverAudit struct {
	calls int
	input state.RecordServerExec
}

func (audit *serverAudit) RecordServerExec(_ context.Context, input state.RecordServerExec) error {
	audit.calls++
	audit.input = input
	return nil
}

func TestServerExecApplicationRequiresUnboundAdminBeforeExecution(t *testing.T) {
	executor := &serverExecutor{}
	audit := &serverAudit{}
	application, err := NewServerExecApplication(executor, audit, bytes.NewReader(make([]byte, 32)), func() time.Time { return time.UnixMilli(10) })
	if err != nil {
		t.Fatal(err)
	}
	project := "project"
	for _, identity := range []Identity{
		{TokenID: "read", Role: "read"},
		{TokenID: "bound", Role: "admin", ProjectID: &project},
	} {
		if _, err := application.Execute(context.Background(), identity, ServerExecInput{Command: "true"}); !errors.Is(err, ErrUnboundAdminRequired) {
			t.Fatalf("identity %+v error = %v", identity, err)
		}
	}
	if executor.calls != 0 || audit.calls != 0 {
		t.Fatalf("dependencies called before authorization: exec=%d audit=%d", executor.calls, audit.calls)
	}
	result, err := application.Execute(context.Background(), Identity{TokenID: "admin", Role: "admin"}, ServerExecInput{Command: "true"})
	if err != nil || result.RequestID == "" || executor.calls != 1 || audit.calls != 1 || audit.input.ActorTokenID != "admin" || !audit.input.Succeeded {
		t.Fatalf("result=%+v exec=%d audit=%+v err=%v", result, executor.calls, audit.input, err)
	}
}
