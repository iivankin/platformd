package containerlogs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadExportsSelectedRangeAsBoundedNDJSON(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "services", "service", "deployment")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(directory, "attempt.log"), strings.Join([]string{
		"2026-07-12T09:59:59Z stdout F before",
		"2026-07-12T10:00:00Z stdout F first",
		"2026-07-12T10:00:01Z stderr P second",
		"2026-07-12T10:00:02Z stdout F after",
	}, "\n")+"\n", time.Now())
	reader, err := NewReader(root)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result, err := reader.Download(context.Background(), DownloadQuery{
		ServiceID: "service", DeploymentID: "deployment",
		From: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 12, 10, 0, 1, 0, time.UTC),
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if result.Records != 2 || result.Truncated || len(lines) != 4 ||
		!strings.Contains(lines[1], `"text":"first"`) || !strings.Contains(lines[2], `"partial":true`) ||
		strings.Contains(output.String(), "before") || strings.Contains(output.String(), "after") {
		t.Fatalf("download = %+v\n%s", result, output.String())
	}
	var footer downloadFooter
	if err := json.Unmarshal([]byte(lines[3]), &footer); err != nil || footer.Type != "platformd.log_export_complete" || footer.Records != 2 {
		t.Fatalf("footer = %+v, err=%v", footer, err)
	}
}

func TestDownloadRejectsRangeOver24HoursBeforeWriting(t *testing.T) {
	reader, err := NewReader(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	from := time.Now().Add(-25 * time.Hour)
	_, err = reader.Download(context.Background(), DownloadQuery{
		ServiceID: "service", From: from, To: from.Add(25 * time.Hour),
	}, &output)
	if !errors.Is(err, ErrInvalidQuery) || output.Len() != 0 {
		t.Fatalf("overlong range = %v, bytes=%d", err, output.Len())
	}
}

func TestReadDownloadLineBoundsOversizedRecord(t *testing.T) {
	line := "2026-07-12T10:00:00Z stdout F " + strings.Repeat("x", 4096) + "\nnext\n"
	reader := bufio.NewReaderSize(strings.NewReader(line), 128)
	value, consumed, oversized, complete, err := readDownloadLine(reader, 256)
	if err != nil || !oversized || !complete || len(value) != 256 || consumed != int64(len(line)-len("next\n")) {
		t.Fatalf("oversized line = len %d, consumed %d, oversized %t, err %v", len(value), consumed, oversized, err)
	}
	next, _, _, complete, err := readDownloadLine(reader, 256)
	if err != nil || !complete || string(next) != "next" {
		t.Fatalf("next line = %q, err=%v", next, err)
	}
}

func TestDownloadDropsConcurrentlyIncompleteTail(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "services", "service", "deployment")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(directory, "attempt.log"), "2026-07-12T10:00:00Z stdout F incomplete", time.Now())
	reader, err := NewReader(root)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result, err := reader.Download(context.Background(), DownloadQuery{
		ServiceID: "service", From: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
		To: time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC),
	}, &output)
	if err != nil || result.Records != 0 || strings.Contains(output.String(), "incomplete") {
		t.Fatalf("incomplete tail = %+v, err=%v, output=%s", result, err, output.String())
	}
}

func TestDownloadWriterRejectsRecordBeforeCrossingByteLimit(t *testing.T) {
	var output bytes.Buffer
	writer := downloadWriter{destination: &output, dataLimit: 32}
	err := writer.record(Record{Text: strings.Repeat("x", 128)})
	if !errors.Is(err, errDownloadLimit) || output.Len() != 0 || writer.result.Records != 0 {
		t.Fatalf("bounded writer = %+v, err=%v, bytes=%d", writer.result, err, output.Len())
	}
}
