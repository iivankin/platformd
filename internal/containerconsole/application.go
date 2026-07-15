package containerconsole

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
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
	ProjectID    string
	ResourceKind string
	ResourceID   string
	Command      []string
	SourceIP     string
	Actor        Actor
	Size         terminaltransport.Size
}

type ResourceRepository interface {
	Resource(context.Context, string, string, string) error
}

type Runtime interface {
	ResourceContainer(string, string) (containerengine.Container, bool, error)
	ExecResourceTerminal(context.Context, string, string, string, containerengine.TerminalExecRequest) (int, error)
	ProbeResourceTerminalShell(context.Context, string, string, string, string) bool
}

func (application *Application) Shells(ctx context.Context, projectID, resourceKind, resourceID string) ([]string, error) {
	if err := application.resources.Resource(ctx, projectID, resourceKind, resourceID); err != nil {
		return nil, err
	}
	target, active, err := application.runtime.ResourceContainer(resourceKind, resourceID)
	if err != nil {
		return nil, fmt.Errorf("inspect service terminal target: %w", err)
	}
	if !active {
		return []string{}, nil
	}
	shells := make([]string, 0, 2)
	for _, shell := range []string{"/bin/sh", "/bin/bash"} {
		if application.runtime.ProbeResourceTerminalShell(ctx, resourceKind, resourceID, target.ID, shell) {
			shells = append(shells, shell)
		}
	}
	return shells, nil
}

type AuditRepository interface {
	AppendTerminalAudit(context.Context, state.TerminalAuditInput) error
}

type Config struct {
	Resources ResourceRepository
	Runtime   Runtime
	Audit     AuditRepository
	Now       func() time.Time
	NewID     func(time.Time) (string, error)
}

type Application struct {
	resources ResourceRepository
	runtime   Runtime
	audit     AuditRepository
	now       func() time.Time
	newID     func(time.Time) (string, error)
}

func New(config Config) (*Application, error) {
	if config.Resources == nil || config.Runtime == nil || config.Audit == nil {
		return nil, errors.New("container console dependencies are incomplete")
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
	return &Application{resources: config.Resources, runtime: config.Runtime, audit: config.Audit, now: now, newID: newID}, nil
}

func (application *Application) Open(ctx context.Context, input OpenInput) (terminaltransport.Session, error) {
	if err := validateOpenInput(input); err != nil {
		return nil, err
	}
	if err := application.resources.Resource(ctx, input.ProjectID, input.ResourceKind, input.ResourceID); err != nil {
		return nil, err
	}
	target, active, err := application.runtime.ResourceContainer(input.ResourceKind, input.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("inspect service terminal target: %w", err)
	}
	if !active {
		return nil, errors.New("service has no running container")
	}
	startedAt := application.now()
	auditID, err := application.newID(startedAt)
	if err != nil {
		return nil, fmt.Errorf("generate terminal audit ID: %w", err)
	}
	if err := application.audit.AppendTerminalAudit(ctx, state.TerminalAuditInput{
		ID: auditID, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		Action: "container_terminal.start", TargetKind: input.ResourceKind, TargetID: input.ResourceID,
		ProjectID: input.ProjectID, ServiceID: serviceAuditID(input), ContainerID: target.ID,
		Command: input.Command, SourceIP: input.SourceIP, Result: "succeeded",
		StartedAtMillis: startedAt.UnixMilli(), CreatedAtMillis: startedAt.UnixMilli(),
	}); err != nil {
		return nil, err
	}
	return newSession(ctx, sessionConfig{
		runtime: application.runtime, resourceKind: input.ResourceKind, resourceID: input.ResourceID, containerID: target.ID,
		command: input.Command, size: input.Size,
		finish: func(reason string, exitCode int, runErr error) error {
			return application.finish(input, target.ID, startedAt, reason, exitCode, runErr)
		},
	}), nil
}

func (application *Application) finish(input OpenInput, containerID string, startedAt time.Time, reason string, exitCode int, runErr error) error {
	finishedAt := application.now()
	auditID, err := application.newID(finishedAt)
	if err != nil {
		return fmt.Errorf("generate terminal completion audit ID: %w", err)
	}
	result := "succeeded"
	errorClass := ""
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		result = "failed"
		errorClass = "terminal_exec_failed"
	}
	var recordedExit *int
	if runErr == nil && exitCode >= 0 {
		recordedExit = &exitCode
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	return application.audit.AppendTerminalAudit(ctx, state.TerminalAuditInput{
		ID: auditID, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		Action: "container_terminal.end", TargetKind: input.ResourceKind, TargetID: input.ResourceID,
		ProjectID: input.ProjectID, ServiceID: serviceAuditID(input), ContainerID: containerID,
		Command: input.Command, SourceIP: input.SourceIP, Result: result,
		StartedAtMillis: startedAt.UnixMilli(), FinishedAtMillis: finishedAt.UnixMilli(),
		DurationMillis: finishedAt.Sub(startedAt).Milliseconds(), CloseReason: reason,
		ExitCode: recordedExit, ErrorClass: errorClass, CreatedAtMillis: finishedAt.UnixMilli(),
	})
}

func validateOpenInput(input OpenInput) error {
	if input.ProjectID == "" || input.ResourceID == "" || !containerResourceKind(input.ResourceKind) || input.Actor.ID == "" || input.Actor.Email == "" || input.SourceIP == "" {
		return errors.New("container console input is incomplete")
	}
	if len(input.Command) == 0 || len(input.Command) > 64 || input.Size.Cols < 1 || input.Size.Rows < 1 {
		return errors.New("container console command or size is invalid")
	}
	total := 0
	for _, argument := range input.Command {
		if argument == "" {
			return errors.New("container console command contains an empty argument")
		}
		total += len(argument)
	}
	if total > 8<<10 {
		return errors.New("container console command is too large")
	}
	return nil
}

func containerResourceKind(kind string) bool {
	return kind == "service" || kind == "postgres" || kind == "redis"
}

func serviceAuditID(input OpenInput) string {
	if input.ResourceKind == "service" {
		return input.ResourceID
	}
	return ""
}

type sessionConfig struct {
	runtime      Runtime
	resourceKind string
	resourceID   string
	containerID  string
	command      []string
	size         terminaltransport.Size
	finish       func(string, int, error) error
}

type session struct {
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	outputReader *io.PipeReader
	outputWriter *io.PipeWriter
	resizes      chan containerengine.TerminalSize
	cancel       context.CancelFunc
	done         chan struct{}
	finish       func(string, int, error) error

	resultMu  sync.Mutex
	exitCode  int
	runErr    error
	closeOnce sync.Once
	closeErr  error
}

func newSession(parent context.Context, config sessionConfig) *session {
	stdinReader, stdinWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	ctx, cancel := context.WithCancel(parent)
	result := &session{
		stdinReader: stdinReader, stdinWriter: stdinWriter,
		outputReader: outputReader, outputWriter: outputWriter,
		resizes: make(chan containerengine.TerminalSize, 1), cancel: cancel,
		done: make(chan struct{}), finish: config.finish, exitCode: -1,
	}
	go func() {
		exitCode, err := config.runtime.ExecResourceTerminal(ctx, config.resourceKind, config.resourceID, config.containerID, containerengine.TerminalExecRequest{
			Command: append([]string(nil), config.command...), Stdin: stdinReader, Output: outputWriter,
			InitialSize: containerengine.TerminalSize{Cols: config.size.Cols, Rows: config.size.Rows},
			Resizes:     result.resizes,
		})
		result.resultMu.Lock()
		result.exitCode = exitCode
		result.runErr = err
		result.resultMu.Unlock()
		_ = outputWriter.CloseWithError(err)
		_ = stdinReader.Close()
		close(result.done)
	}()
	return result
}

func (session *session) Read(target []byte) (int, error) {
	return session.outputReader.Read(target)
}

func (session *session) Write(payload []byte) (int, error) {
	return session.stdinWriter.Write(payload)
}

func (session *session) Resize(size terminaltransport.Size) error {
	resize := containerengine.TerminalSize{Cols: size.Cols, Rows: size.Rows}
	select {
	case session.resizes <- resize:
		return nil
	default:
	}
	select {
	case <-session.resizes:
	default:
	}
	select {
	case session.resizes <- resize:
		return nil
	case <-session.done:
		return errors.New("terminal process has exited")
	}
}

func (session *session) Wait() (int, error) {
	<-session.done
	session.resultMu.Lock()
	defer session.resultMu.Unlock()
	return session.exitCode, session.runErr
}

func (session *session) Close(reason string) error {
	session.closeOnce.Do(func() {
		session.cancel()
		_ = session.stdinWriter.Close()
		_ = session.outputReader.Close()
		<-session.done
		exitCode, runErr := session.result()
		session.closeErr = session.finish(reason, exitCode, runErr)
	})
	return session.closeErr
}

func (session *session) result() (int, error) {
	session.resultMu.Lock()
	defer session.resultMu.Unlock()
	return session.exitCode, session.runErr
}
