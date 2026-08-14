package systemevent

import (
	"bytes"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
)

func TestEventKeepsOneBoundedJournalLine(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	Failure("Deployment Failed", errors.New("first\nsecond"), String("Service ID", "service\t1"))
	message := output.String()
	if strings.Count(message, "\n") != 1 ||
		!strings.Contains(message, `level=error event=deploymentfailed`) ||
		!strings.Contains(message, `serviceid="service 1"`) ||
		!strings.Contains(message, `error="first second"`) {
		t.Fatalf("event = %q", message)
	}
}

func TestLineWriterEmitsBoundedJournalRecords(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	writer := NewLineWriter("postgres_extension_build", String("postgres_id", "database"))
	if _, err := io.WriteString(writer, "installing\ncompiling"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	message := output.String()
	if strings.Count(message, "\n") != 2 ||
		!strings.Contains(message, `event=postgres_extension_build postgres_id="database" message="installing"`) ||
		!strings.Contains(message, `event=postgres_extension_build postgres_id="database" message="compiling"`) {
		t.Fatalf("events = %q", message)
	}
}
