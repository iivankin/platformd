package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type logServices struct {
	calls int
}

func (services *logServices) Service(context.Context, string, string) (state.ServiceDesired, error) {
	services.calls++
	return state.ServiceDesired{}, nil
}

type logReader struct {
	calls int
	query containerlogs.Query
}

func (reader *logReader) Read(_ context.Context, query containerlogs.Query) (containerlogs.Window, error) {
	reader.calls++
	reader.query = query
	return containerlogs.Window{Records: []containerlogs.Record{{Text: "ready"}}}, nil
}

func TestLogApplicationAuthorizesBeforeServiceLookup(t *testing.T) {
	services := &logServices{}
	reader := &logReader{}
	application, err := NewLogApplication(services, reader)
	if err != nil {
		t.Fatal(err)
	}
	input := ReadServiceLogsInput{ProjectID: "project", ServiceID: "service", Limit: 10}
	if _, err := application.ReadService(context.Background(), Identity{TokenID: "token", Role: "unknown"}, input); !errors.Is(err, ErrReadTokenRequired) {
		t.Fatalf("role error = %v", err)
	}
	bound := "other"
	if _, err := application.ReadService(context.Background(), Identity{TokenID: "token", Role: "read", ProjectID: &bound}, input); !errors.Is(err, ErrProjectBoundary) {
		t.Fatalf("boundary error = %v", err)
	}
	if services.calls != 0 || reader.calls != 0 {
		t.Fatalf("dependencies called before authorization: services=%d reader=%d", services.calls, reader.calls)
	}
	window, err := application.ReadService(context.Background(), Identity{TokenID: "token", Role: "read"}, input)
	if err != nil || len(window.Records) != 1 || services.calls != 1 || reader.calls != 1 || reader.query.ServiceID != "service" {
		t.Fatalf("read = %+v, services=%d reader=%d query=%+v err=%v", window, services.calls, reader.calls, reader.query, err)
	}
}
