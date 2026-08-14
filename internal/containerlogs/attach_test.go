package containerlogs

import (
	"context"
	"errors"
	"io"
	"testing"
)

type failingAttachEngine struct {
	err error
}

func (engine failingAttachEngine) StartContainerAttached(
	context.Context,
	string,
	io.WriteCloser,
	io.WriteCloser,
) (<-chan error, error) {
	return nil, engine.err
}

type trackingSink struct {
	writers []*trackingWriter
}

func (sink *trackingSink) ContainerWriter(_, _, _, _, _ string) io.WriteCloser {
	writer := &trackingWriter{}
	sink.writers = append(sink.writers, writer)
	return writer
}

type trackingWriter struct {
	closed bool
}

func (*trackingWriter) Write(value []byte) (int, error) { return len(value), nil }

func (writer *trackingWriter) Close() error {
	writer.closed = true
	return nil
}

func TestStartAttachedClosesWritersWhenAttachFails(t *testing.T) {
	t.Parallel()

	attachErr := errors.New("attach failed")
	sink := &trackingSink{}
	_, err := StartAttached(context.Background(), failingAttachEngine{err: attachErr}, sink, RuntimeMetadata{
		ResourceID: "service-id", ResourceName: "api", DeploymentID: "deployment-id", ContainerID: "container-id",
	})
	if !errors.Is(err, attachErr) {
		t.Fatalf("StartAttached() error = %v, want %v", err, attachErr)
	}
	if len(sink.writers) != 2 || !sink.writers[0].closed || !sink.writers[1].closed {
		t.Fatalf("StartAttached() writers = %#v, want both closed", sink.writers)
	}
}
