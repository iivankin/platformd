package rootexec

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

type testLeaf struct{}

func (testLeaf) FD() uintptr                 { return 1 }
func (testLeaf) Kill() error                 { return nil }
func (testLeaf) Close(context.Context) error { return nil }

func TestRunnerRejectsInvalidRequestsBeforeCreatingCgroup(t *testing.T) {
	created := 0
	runner, err := New(Config{
		CreateLeaf: func(string) (Leaf, error) { created++; return testLeaf{}, nil },
		Random:     bytes.NewReader(make([]byte, 32)), Now: time.Now,
		CommandBytes: 8, OutputBytes: 16, DefaultTimeout: time.Second,
		MaximumTimeout: 2 * time.Second, MaximumParallel: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{{}, {Command: "123456789"}, {Command: "bad\x00"}, {Command: "ok", Timeout: 3 * time.Second}} {
		if _, err := runner.Execute(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %+v error = %v", request, err)
		}
	}
	if created != 0 {
		t.Fatalf("created %d cgroups for invalid requests", created)
	}
}

func TestBoundedBufferKeepsPrefixWithoutShortWrites(t *testing.T) {
	buffer := newBoundedBuffer(5)
	if count, err := buffer.Write([]byte("abcdefgh")); err != nil || count != 8 {
		t.Fatalf("write = %d, %v", count, err)
	}
	if value, truncated := buffer.Result(); value != "abcde" || !truncated {
		t.Fatalf("result = %q/%t", value, truncated)
	}
}
