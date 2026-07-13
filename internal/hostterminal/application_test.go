package hostterminal

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

type auditMemory struct {
	entries []state.TerminalAuditInput
}

func (memory *auditMemory) AppendTerminalAudit(_ context.Context, input state.TerminalAuditInput) error {
	memory.entries = append(memory.entries, input)
	return nil
}

type unusedLeaf struct{}

func (unusedLeaf) FD() uintptr                 { return 1 }
func (unusedLeaf) Kill() error                 { return nil }
func (unusedLeaf) Close(context.Context) error { return nil }

type memorySession struct {
	finish func(string, int, error) error
}

func (session *memorySession) Read([]byte) (int, error)            { return 0, io.EOF }
func (session *memorySession) Write(value []byte) (int, error)     { return len(value), nil }
func (session *memorySession) Resize(terminaltransport.Size) error { return nil }
func (session *memorySession) Wait() (int, error)                  { return 0, nil }
func (session *memorySession) Close(reason string) error           { return session.finish(reason, 0, nil) }

func TestApplicationAuditsHostTerminalMetadataWithoutContent(t *testing.T) {
	memory := &auditMemory{}
	startedAt := time.Unix(1_900_000_000, 0)
	nowCalls := 0
	application, err := New(Config{
		Audit: memory, InstallationID: "installation",
		CreateLeaf: func(string) (Leaf, error) { return unusedLeaf{}, nil },
		Now: func() time.Time {
			nowCalls++
			return startedAt.Add(time.Duration(nowCalls-1) * time.Second)
		},
		NewID: func(timestamp time.Time) (string, error) { return timestamp.Format(time.RFC3339), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	application.spawn = func(_ context.Context, config spawnConfig) (terminaltransport.Session, error) {
		if config.leafID != "terminal-"+startedAt.Format(time.RFC3339) || config.size != (terminaltransport.Size{Cols: 120, Rows: 40}) {
			t.Fatalf("spawn config = %+v", config)
		}
		return &memorySession{finish: config.finish}, nil
	}
	session, err := application.Open(context.Background(), OpenInput{
		Actor:    Actor{ID: "subject", Email: "admin@example.com"},
		SourceIP: "203.0.113.9", Size: terminaltransport.Size{Cols: 120, Rows: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close("client_closed"); err != nil {
		t.Fatal(err)
	}
	if len(memory.entries) != 2 {
		t.Fatalf("audit count = %d", len(memory.entries))
	}
	start, finish := memory.entries[0], memory.entries[1]
	if start.Action != "server_terminal.start" || start.TargetID != "installation" || len(start.Command) != 0 ||
		finish.Action != "server_terminal.end" || finish.CloseReason != "client_closed" || finish.DurationMillis != 1000 ||
		finish.ExitCode == nil || *finish.ExitCode != 0 || len(finish.Command) != 0 {
		t.Fatalf("audit entries = %+v / %+v", start, finish)
	}
}

func TestApplicationRecordsSpawnFailure(t *testing.T) {
	memory := &auditMemory{}
	application, err := New(Config{
		Audit: memory, InstallationID: "installation",
		CreateLeaf: func(string) (Leaf, error) { return unusedLeaf{}, nil },
		NewID:      func(time.Time) (string, error) { return time.Now().String(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	application.spawn = func(context.Context, spawnConfig) (terminaltransport.Session, error) {
		return nil, errors.New("pty unavailable")
	}
	_, err = application.Open(context.Background(), OpenInput{
		Actor:    Actor{ID: "subject", Email: "admin@example.com"},
		SourceIP: "203.0.113.9", Size: terminaltransport.Size{Cols: 80, Rows: 24},
	})
	if err == nil || len(memory.entries) != 2 || memory.entries[1].Result != "failed" || memory.entries[1].CloseReason != "spawn_failed" {
		t.Fatalf("spawn failure = %v, audit = %+v", err, memory.entries)
	}
}
