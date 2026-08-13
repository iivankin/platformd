package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerlogs"
)

func TestTelemetryLogReaderReturnsChronologicalServiceWindow(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/internal/logs" || request.URL.Query().Get("serviceId") != "service" ||
			request.URL.Query().Get("deploymentId") != "deployment" || request.URL.Query().Get("contains") != "ready" ||
			request.URL.Query().Get("severityText") != "error" ||
			request.URL.Query().Get("traceId") != "0123456789abcdef0123456789abcdef" ||
			request.URL.Query().Get("spanId") != "0123456789abcdef" ||
			request.URL.Query().Get("from") != "1000" || request.URL.Query().Get("to") != "2000" ||
			request.URL.Query().Get("order") != "asc" {
			t.Errorf("unexpected telemetry log request: %s", request.URL.String())
		}
		_ = json.NewEncoder(response).Encode(telemetryLogPage{
			Records: []telemetryLogRecord{{
				ID: "00000000-0000-4000-8000-000000000001", TimeUnixNano: 10,
				Stream: "stdout", Text: "ready", DeploymentID: "deployment", AttemptID: "attempt",
				TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
				SeverityText: "info", SeverityNumber: 9,
			}},
			Revision: "10:00000000-0000-4000-8000-000000000001",
		})
	}))
	defer server.Close()
	reader, err := NewLogReader(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reader.Read(context.Background(), containerlogs.Query{
		ServiceID: "service", DeploymentID: "deployment", Contains: "ready", SeverityText: "error",
		TraceID: "0123456789ABCDEF0123456789ABCDEF", SpanID: "0123456789ABCDEF",
		From: time.UnixMilli(1000), To: time.UnixMilli(2000), Limit: 20, Ascending: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Records) != 1 || window.Records[0].Text != "ready" ||
		window.Records[0].DeploymentID != "deployment" || window.Records[0].AttemptID != "attempt" ||
		window.Records[0].TraceID != "0123456789abcdef0123456789abcdef" ||
		window.Records[0].SpanID != "0123456789abcdef" || window.Records[0].SeverityText != "info" ||
		window.Records[0].SeverityNumber != 9 {
		t.Fatalf("telemetry log window = %+v", window)
	}
}

func TestTelemetryLogDownloadFollowsInternalCursor(t *testing.T) {
	t.Parallel()
	const firstID = "00000000-0000-4000-8000-000000000001"
	const secondID = "00000000-0000-4000-8000-000000000002"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("order") != "asc" {
			t.Errorf("download order = %q", query.Get("order"))
		}
		if query.Get("afterId") == "" {
			nextTime := uint64(10)
			nextID := firstID
			_ = json.NewEncoder(response).Encode(telemetryLogPage{
				Records:   []telemetryLogRecord{{ID: firstID, TimeUnixNano: 10, Stream: "stdout", Text: "one"}},
				Truncated: true, NextTimeUnixNano: &nextTime, NextID: &nextID,
			})
			return
		}
		_ = json.NewEncoder(response).Encode(telemetryLogPage{
			Records: []telemetryLogRecord{{ID: secondID, TimeUnixNano: 20, Stream: "stderr", Text: "two"}},
		})
	}))
	defer server.Close()
	reader, err := NewLogReader(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, err = reader.Download(context.Background(), containerlogs.DownloadQuery{
		ServiceID: "service", From: time.Unix(0, 1), To: time.Unix(0, 100),
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	content := output.String()
	if strings.Count(content, `"type":"record"`) != 2 || !strings.Contains(content, `"text":"one"`) ||
		!strings.Contains(content, `"text":"two"`) || !strings.Contains(content, `"records":2`) {
		t.Fatalf("telemetry log download = %s", content)
	}
}

func TestLineWriterBoundsUnterminatedRecords(t *testing.T) {
	t.Parallel()
	exporter := &LogExporter{queue: make(chan logRecord, 4)}
	writer := &lineWriter{exporter: exporter, metadata: LogMetadata{ServiceID: "service"}}
	input := bytes.Repeat([]byte{'x'}, maximumLogBytes+3)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	first := <-exporter.queue
	second := <-exporter.queue
	if len(first.body) != maximumLogBytes || !first.partial || string(second.body) != "xxx" || !second.partial {
		t.Fatalf("bounded records = %d/%t and %q/%t", len(first.body), first.partial, second.body, second.partial)
	}
}

func TestLineWriterDoesNotEmitEmptyTerminatorAfterExactChunk(t *testing.T) {
	t.Parallel()
	exporter := &LogExporter{queue: make(chan logRecord, 2)}
	writer := &lineWriter{exporter: exporter, metadata: LogMetadata{ServiceID: "service"}}
	if _, err := writer.Write(bytes.Repeat([]byte{'x'}, maximumLogBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte{'\n'}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	first := <-exporter.queue
	if len(first.body) != maximumLogBytes || !first.partial {
		t.Fatalf("bounded record = %d/%t", len(first.body), first.partial)
	}
	select {
	case extra := <-exporter.queue:
		t.Fatalf("unexpected trailing record = %q/%t", extra.body, extra.partial)
	default:
	}
}
