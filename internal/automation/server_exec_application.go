package automation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/rootexec"
	"github.com/iivankin/platformd/internal/state"
)

var ErrUnboundAdminRequired = errors.New("unbound admin token is required")

type ServerExecutor interface {
	Execute(context.Context, rootexec.Request) (rootexec.Result, error)
}

type ServerExecAudit interface {
	RecordServerExec(context.Context, state.RecordServerExec) error
}

type ServerExecApplication struct {
	executor ServerExecutor
	audit    ServerExecAudit
	random   io.Reader
	now      func() time.Time
}

type ServerExecInput struct {
	Command        string
	TimeoutSeconds int
}

type ServerExecResult struct {
	Execution rootexec.Result
	RequestID string
}

func NewServerExecApplication(executor ServerExecutor, audit ServerExecAudit, random io.Reader, now func() time.Time) (*ServerExecApplication, error) {
	if executor == nil || audit == nil {
		return nil, errors.New("server exec application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &ServerExecApplication{executor: executor, audit: audit, random: random, now: now}, nil
}

func (application *ServerExecApplication) Execute(ctx context.Context, identity Identity, input ServerExecInput) (ServerExecResult, error) {
	if !identity.IsAdmin() || identity.ProjectID != nil || identity.TokenID == "" {
		return ServerExecResult{}, ErrUnboundAdminRequired
	}
	timestamp := application.now()
	auditID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return ServerExecResult{}, fmt.Errorf("allocate server exec audit ID: %w", err)
	}
	requestID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return ServerExecResult{}, fmt.Errorf("allocate server exec request ID: %w", err)
	}
	result, executionErr := application.executor.Execute(ctx, rootexec.Request{
		Command: input.Command, Timeout: time.Duration(input.TimeoutSeconds) * time.Second,
	})
	if result.StartedAt <= 0 {
		result.StartedAt = timestamp.UnixMilli()
	}
	if result.FinishedAt < result.StartedAt {
		result.FinishedAt = application.now().UnixMilli()
	}
	result.DurationMillis = max(0, result.FinishedAt-result.StartedAt)
	// Cancellation must not erase the audit record for a command that already
	// reached the host. The write remains tightly bounded and stores no command
	// text or output.
	auditContext, cancelAudit := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	auditErr := application.audit.RecordServerExec(auditContext, state.RecordServerExec{
		AuditEventID: auditID, ActorTokenID: identity.TokenID, RequestCorrelationID: requestID,
		Succeeded:       executionErr == nil && result.ExitCode == 0 && !result.TimedOut && !result.Cancelled,
		StartedAtMillis: result.StartedAt, FinishedAtMillis: result.FinishedAt,
		DurationMillis: result.DurationMillis, ExitCode: result.ExitCode,
		TimedOut: result.TimedOut, Cancelled: result.Cancelled,
		StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated,
		ExecutionError: executionErr != nil,
	})
	cancelAudit()
	return ServerExecResult{Execution: result, RequestID: requestID}, errors.Join(executionErr, auditErr)
}
