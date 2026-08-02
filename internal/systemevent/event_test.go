package systemevent

import (
	"bytes"
	"errors"
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
