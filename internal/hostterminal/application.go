package hostterminal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

const auditTimeout = 5 * time.Second

type Actor struct {
	ID    string
	Email string
}

type OpenInput struct {
	Actor    Actor
	SourceIP string
	Size     terminaltransport.Size
}

type AuditRepository interface {
	AppendTerminalAudit(context.Context, state.TerminalAuditInput) error
}

type Leaf interface {
	FD() uintptr
	Kill() error
	Close(context.Context) error
}

type Config struct {
	Audit          AuditRepository
	CreateLeaf     func(string) (Leaf, error)
	InstallationID string
	Now            func() time.Time
	NewID          func(time.Time) (string, error)
}

type Application struct {
	audit          AuditRepository
	createLeaf     func(string) (Leaf, error)
	installationID string
	now            func() time.Time
	newID          func(time.Time) (string, error)
	spawn          spawn
}

func New(config Config) (*Application, error) {
	if config.Audit == nil || config.CreateLeaf == nil || config.InstallationID == "" {
		return nil, errors.New("host terminal dependencies are incomplete")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = func(timestamp time.Time) (string, error) {
			return id.NewWith(timestamp, rand.Reader)
		}
	}
	return &Application{
		audit: config.Audit, createLeaf: config.CreateLeaf, installationID: config.InstallationID,
		now: now, newID: newID, spawn: spawnPTY,
	}, nil
}

func (application *Application) Open(ctx context.Context, input OpenInput) (terminaltransport.Session, error) {
	if err := validateOpenInput(input); err != nil {
		return nil, err
	}
	startedAt := application.now()
	sessionID, err := application.newID(startedAt)
	if err != nil {
		return nil, fmt.Errorf("generate host terminal session ID: %w", err)
	}
	if err := application.appendAudit(ctx, state.TerminalAuditInput{
		ID: sessionID, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		Action: "server_terminal.start", TargetKind: "installation", TargetID: application.installationID,
		SourceIP: input.SourceIP, Result: "succeeded", StartedAtMillis: startedAt.UnixMilli(),
		CreatedAtMillis: startedAt.UnixMilli(),
	}); err != nil {
		return nil, err
	}
	session, err := application.spawn(ctx, spawnConfig{
		createLeaf: application.createLeaf, leafID: "terminal-" + sessionID, size: input.Size,
		finish: func(reason string, exitCode int, runErr error) error {
			return application.finish(input, startedAt, reason, exitCode, runErr)
		},
	})
	if err == nil {
		return session, nil
	}
	finishErr := application.finish(input, startedAt, "spawn_failed", -1, err)
	return nil, errors.Join(err, finishErr)
}

func (application *Application) finish(input OpenInput, startedAt time.Time, reason string, exitCode int, runErr error) error {
	finishedAt := application.now()
	auditID, err := application.newID(finishedAt)
	if err != nil {
		return fmt.Errorf("generate host terminal completion audit ID: %w", err)
	}
	result := "succeeded"
	errorClass := ""
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		result = "failed"
		errorClass = "host_terminal_failed"
	}
	var recordedExit *int
	if runErr == nil && exitCode >= 0 {
		recordedExit = &exitCode
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	return application.appendAudit(ctx, state.TerminalAuditInput{
		ID: auditID, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		Action: "server_terminal.end", TargetKind: "installation", TargetID: application.installationID,
		SourceIP: input.SourceIP, Result: result, StartedAtMillis: startedAt.UnixMilli(),
		FinishedAtMillis: finishedAt.UnixMilli(), DurationMillis: max(0, finishedAt.Sub(startedAt).Milliseconds()),
		CloseReason: reason, ExitCode: recordedExit, ErrorClass: errorClass, CreatedAtMillis: finishedAt.UnixMilli(),
	})
}

func (application *Application) appendAudit(ctx context.Context, input state.TerminalAuditInput) error {
	if err := application.audit.AppendTerminalAudit(ctx, input); err != nil {
		return fmt.Errorf("append host terminal audit: %w", err)
	}
	return nil
}

func validateOpenInput(input OpenInput) error {
	if input.Actor.ID == "" || input.Actor.Email == "" || input.SourceIP == "" ||
		input.Size.Cols < 1 || input.Size.Cols > 1000 || input.Size.Rows < 1 || input.Size.Rows > 500 {
		return errors.New("host terminal input is incomplete")
	}
	return nil
}

type spawn func(context.Context, spawnConfig) (terminaltransport.Session, error)

type spawnConfig struct {
	createLeaf func(string) (Leaf, error)
	leafID     string
	size       terminaltransport.Size
	finish     func(string, int, error) error
}
