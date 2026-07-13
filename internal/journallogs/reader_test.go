package journallogs

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type runnerStub struct {
	arguments []string
	output    string
	err       error
}

func (runner *runnerStub) Run(_ context.Context, arguments []string, stdout, _ io.Writer) error {
	runner.arguments = append([]string(nil), arguments...)
	_, _ = io.WriteString(stdout, runner.output)
	return runner.err
}

func TestReaderUsesFixedUnitAndReturnsBoundedNewestRecords(t *testing.T) {
	runner := &runnerStub{output: strings.Join([]string{
		`{"__CURSOR":"cursor-3","__REALTIME_TIMESTAMP":"3000000","PRIORITY":"3","MESSAGE":"newest","SYSLOG_IDENTIFIER":"platformd","_PID":"42"}`,
		`{"__CURSOR":"cursor-2","__REALTIME_TIMESTAMP":"2000000","PRIORITY":"6","MESSAGE":"middle"}`,
		`{"__CURSOR":"cursor-1","__REALTIME_TIMESTAMP":"1000000","PRIORITY":"6","MESSAGE":"oldest"}`,
	}, "\n") + "\n"}
	reader, err := NewReaderWithRunner(runner)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reader.Read(context.Background(), Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Records) != 2 || !window.Truncated || window.Records[0].Message != "newest" ||
		window.Records[0].Identifier != "platformd" || window.Records[0].PID != "42" {
		t.Fatalf("window = %+v", window)
	}
	arguments := strings.Join(runner.arguments, " ")
	if !strings.Contains(arguments, "--unit=platformd.service") || !strings.Contains(arguments, "--output=json") ||
		!strings.Contains(arguments, "--reverse") || !strings.Contains(arguments, "--lines=3") {
		t.Fatalf("journalctl arguments = %v", runner.arguments)
	}
}

func TestReaderSanitizesMessageAndRejectsInvalidQueryBeforeSpawn(t *testing.T) {
	runner := &runnerStub{output: `{"__CURSOR":"cursor","__REALTIME_TIMESTAMP":"1000000","PRIORITY":"4","MESSAGE":"bad\u001b\u0000"}` + "\n"}
	reader, err := NewReaderWithRunner(runner)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reader.Read(context.Background(), Query{Limit: 1})
	if err != nil || window.Records[0].Message != "bad��" {
		t.Fatalf("sanitized window = %+v, err=%v", window, err)
	}
	runner.arguments = nil
	_, err = reader.Read(context.Background(), Query{Limit: MaximumLimit + 1})
	if !errors.Is(err, ErrInvalidQuery) || runner.arguments != nil {
		t.Fatalf("invalid query = %v, arguments=%v", err, runner.arguments)
	}
}

func TestBoundedBufferDiscardsExcessWithoutShortWrite(t *testing.T) {
	buffer := &boundedBuffer{maximum: 4}
	written, err := buffer.Write([]byte("123456"))
	if err != nil || written != 6 || buffer.String() != "1234" || !buffer.truncated {
		t.Fatalf("bounded buffer = %q/%t, written=%d, err=%v", buffer.String(), buffer.truncated, written, err)
	}
}
