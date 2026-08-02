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
	calls     [][]string
	output    string
	outputs   []string
	err       error
}

func (runner *runnerStub) Run(_ context.Context, arguments []string, stdout, _ io.Writer) error {
	runner.arguments = append([]string(nil), arguments...)
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	output := runner.output
	if len(runner.outputs) >= len(runner.calls) {
		output = runner.outputs[len(runner.calls)-1]
	}
	_, _ = io.WriteString(stdout, output)
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
	if len(window.Records) != 2 || window.NextCursor != "cursor-2" || window.Records[0].Message != "newest" ||
		window.Records[0].Identifier != "platformd" || window.Records[0].PID != "42" {
		t.Fatalf("window = %+v", window)
	}
	var calledArguments []string
	for _, call := range runner.calls {
		calledArguments = append(calledArguments, call...)
	}
	arguments := strings.Join(calledArguments, " ")
	if !strings.Contains(arguments, "--unit=platformd.service") || !strings.Contains(arguments, "--output=json") ||
		!strings.Contains(arguments, "CONTAINER_NAME=platformd-cloudflare-mesh") ||
		!strings.Contains(arguments, "--dmesg") || !strings.Contains(arguments, "_PID=1") ||
		!strings.Contains(arguments, "--reverse") || !strings.Contains(arguments, "--lines=3") {
		t.Fatalf("journalctl calls = %v", runner.calls)
	}
}

func TestReaderMergesPlatformKernelAndSystemdRecordsByTime(t *testing.T) {
	runner := &runnerStub{outputs: []string{
		`{"__CURSOR":"platform","__REALTIME_TIMESTAMP":"2000000","PRIORITY":"6","MESSAGE":"platform","SYSLOG_IDENTIFIER":"platformd"}` + "\n",
		`{"__CURSOR":"mesh","__REALTIME_TIMESTAMP":"4000000","PRIORITY":"6","MESSAGE":"mesh","SYSLOG_IDENTIFIER":"container","CONTAINER_NAME":"platformd-cloudflare-mesh"}` + "\n",
		`{"__CURSOR":"kernel","__REALTIME_TIMESTAMP":"3000000","PRIORITY":"3","MESSAGE":"oom","SYSLOG_IDENTIFIER":"kernel"}` + "\n",
		`{"__CURSOR":"systemd","__REALTIME_TIMESTAMP":"1000000","PRIORITY":"4","MESSAGE":"unit failed","SYSLOG_IDENTIFIER":"systemd"}` + "\n",
	}}
	reader, err := NewReaderWithRunner(runner)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reader.Read(context.Background(), Query{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Records) != 4 || window.Records[0].Cursor != "mesh" ||
		window.Records[0].Identifier != "platformd-cloudflare-mesh" || window.Records[1].Cursor != "kernel" ||
		window.Records[2].Cursor != "platform" || window.Records[3].Cursor != "systemd" {
		t.Fatalf("records = %+v", window.Records)
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
	runner.calls = nil
	_, err = reader.Read(context.Background(), Query{Limit: MaximumLimit + 1})
	if !errors.Is(err, ErrInvalidQuery) || runner.arguments != nil || runner.calls != nil {
		t.Fatalf("invalid query = %v, arguments=%v, calls=%v", err, runner.arguments, runner.calls)
	}
	runner.arguments = nil
	runner.calls = nil
	_, err = reader.Read(context.Background(), Query{Limit: 1, BeforeCursor: "bad\ncursor"})
	if !errors.Is(err, ErrInvalidQuery) || runner.arguments != nil || runner.calls != nil {
		t.Fatalf("invalid cursor query = %v, arguments=%v, calls=%v", err, runner.arguments, runner.calls)
	}
}

func TestReaderPagesBackwardFromOpaqueCursor(t *testing.T) {
	runner := &runnerStub{outputs: []string{
		strings.Join([]string{
			`{"__CURSOR":"platform-2","__REALTIME_TIMESTAMP":"2000000","PRIORITY":"6","MESSAGE":"platform 2"}`,
			`{"__CURSOR":"platform-1","__REALTIME_TIMESTAMP":"1000000","PRIORITY":"6","MESSAGE":"platform 1"}`,
		}, "\n") + "\n",
		`{"__CURSOR":"mesh-3","__REALTIME_TIMESTAMP":"3000000","PRIORITY":"6","MESSAGE":"mesh 3"}` + "\n",
		"",
		"",
	}}
	reader, err := NewReaderWithRunner(runner)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reader.Read(context.Background(), Query{Limit: 2, BeforeCursor: "opaque;cursor=value"})
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Records) != 2 || window.Records[0].Cursor != "mesh-3" ||
		window.Records[1].Cursor != "platform-2" || window.NextCursor != "platform-2" {
		t.Fatalf("window = %+v", window)
	}
	for _, arguments := range runner.calls {
		if !containsArgument(arguments, "--after-cursor=opaque;cursor=value") ||
			!containsArgument(arguments, "--reverse") || !containsArgument(arguments, "--lines=3") {
			t.Fatalf("journalctl arguments = %v", arguments)
		}
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func TestSystemEventLevelSetsDisplayedPriority(t *testing.T) {
	record, err := parseRecord([]byte(`{"__CURSOR":"cursor","__REALTIME_TIMESTAMP":"1000000","PRIORITY":"6","MESSAGE":"level=error event=deployment_failed error=boom"}`))
	if err != nil {
		t.Fatal(err)
	}
	if record.Priority != 3 {
		t.Fatalf("priority = %d, want error", record.Priority)
	}
}

func TestBoundedBufferDiscardsExcessWithoutShortWrite(t *testing.T) {
	buffer := &boundedBuffer{maximum: 4}
	written, err := buffer.Write([]byte("123456"))
	if err != nil || written != 6 || buffer.String() != "1234" || !buffer.truncated {
		t.Fatalf("bounded buffer = %q/%t, written=%d, err=%v", buffer.String(), buffer.truncated, written, err)
	}
}
