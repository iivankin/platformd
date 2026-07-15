package containerconsole_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerconsole"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

type resourceRepository struct{}

func (resourceRepository) Resource(_ context.Context, _, _, _ string) error {
	return nil
}

type runtimeStub struct {
	input   chan string
	resized chan containerengine.TerminalSize
}

func (runtime *runtimeStub) ResourceContainer(_, _ string) (containerengine.Container, bool, error) {
	return containerengine.Container{ID: "container", State: "running"}, true, nil
}

func (runtime *runtimeStub) ExecResourceTerminal(ctx context.Context, _, _, containerID string, request containerengine.TerminalExecRequest) (int, error) {
	if containerID != "container" || request.InitialSize != (containerengine.TerminalSize{Cols: 100, Rows: 30}) {
		return -1, io.ErrUnexpectedEOF
	}
	_, _ = request.Output.Write([]byte("ready\r\n"))
	buffer := make([]byte, 32)
	count, err := request.Stdin.Read(buffer)
	if err != nil {
		return -1, err
	}
	runtime.input <- string(buffer[:count])
	select {
	case size := <-request.Resizes:
		runtime.resized <- size
	case <-ctx.Done():
		return -1, ctx.Err()
	}
	<-ctx.Done()
	return -1, ctx.Err()
}

func (runtime *runtimeStub) ProbeResourceTerminalShell(_ context.Context, _, _, _ string, shell string) bool {
	return shell == "/bin/sh"
}

type auditRepository struct {
	mu     sync.Mutex
	events []state.TerminalAuditInput
}

func (repository *auditRepository) AppendTerminalAudit(_ context.Context, input state.TerminalAuditInput) error {
	repository.mu.Lock()
	repository.events = append(repository.events, input)
	repository.mu.Unlock()
	return nil
}

func TestContainerConsoleOwnsExactExecSessionAndAuditsLifecycle(t *testing.T) {
	t.Parallel()

	runtime := &runtimeStub{input: make(chan string, 1), resized: make(chan containerengine.TerminalSize, 1)}
	audit := &auditRepository{}
	times := []time.Time{time.UnixMilli(1_000), time.UnixMilli(2_500)}
	var timeMu sync.Mutex
	application, err := containerconsole.New(containerconsole.Config{
		Resources: resourceRepository{}, Runtime: runtime, Audit: audit,
		Now: func() time.Time {
			timeMu.Lock()
			defer timeMu.Unlock()
			result := times[0]
			times = times[1:]
			return result
		},
		NewID: func(timestamp time.Time) (string, error) { return timestamp.Format(time.RFC3339Nano), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := application.Open(context.Background(), containerconsole.OpenInput{
		ProjectID: "project", ResourceKind: "service", ResourceID: "service", Command: []string{"/bin/sh"},
		SourceIP: "203.0.113.9", Actor: containerconsole.Actor{ID: "subject", Email: "admin@example.com"},
		Size: terminaltransport.Size{Cols: 100, Rows: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := make([]byte, len("ready\r\n"))
	if _, err := io.ReadFull(session, output); err != nil || string(output) != "ready\r\n" {
		t.Fatalf("terminal output = %q, %v", output, err)
	}
	if _, err := session.Write([]byte("whoami\r")); err != nil {
		t.Fatal(err)
	}
	if err := session.Resize(terminaltransport.Size{Cols: 132, Rows: 44}); err != nil {
		t.Fatal(err)
	}
	if input := <-runtime.input; input != "whoami\r" {
		t.Fatalf("terminal input = %q", input)
	}
	if size := <-runtime.resized; size != (containerengine.TerminalSize{Cols: 132, Rows: 44}) {
		t.Fatalf("terminal resize = %+v", size)
	}
	if err := session.Close("client_closed"); err != nil {
		t.Fatal(err)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.events) != 2 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	started, finished := audit.events[0], audit.events[1]
	if started.Action != "container_terminal.start" || started.ContainerID != "container" || started.StartedAtMillis != 1_000 {
		t.Fatalf("start audit = %+v", started)
	}
	if finished.Action != "container_terminal.end" || finished.Result != "succeeded" || finished.CloseReason != "client_closed" || finished.DurationMillis != 1_500 || finished.ErrorClass != "" {
		t.Fatalf("finish audit = %+v", finished)
	}
}
