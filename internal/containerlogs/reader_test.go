package containerlogs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReaderReturnsBoundedSanitizedRecordsAcrossAttempts(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "services", "service", "deployment")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(directory, "attempt-a.log.1"), strings.Join([]string{
		"2026-07-12T10:00:00.000000001Z stdout F first",
		"2026-07-12T10:00:01.000000001Z stderr P partial",
	}, "\n")+"\n", time.Unix(1, 0))
	unsafe := append([]byte("2026-07-12T10:00:02.000000001Z stdout F bad"), 0xff, 0x1b, 0x00)
	unsafe = append(unsafe, '\n')
	if err := os.WriteFile(filepath.Join(directory, "attempt-a.log"), unsafe, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(directory, "attempt-a.log"), time.Unix(2, 0), time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}

	reader, err := NewReader(root)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reader.Read(context.Background(), Query{ServiceID: "service", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Records) != 3 || window.Records[0].Text != "first" || !window.Records[1].Partial {
		t.Fatalf("records = %+v", window.Records)
	}
	if window.Records[2].Text != "bad���" || strings.ContainsRune(window.Records[2].Text, '\x1b') || strings.ContainsRune(window.Records[2].Text, '\x00') {
		t.Fatalf("unsafe text = %q", window.Records[2].Text)
	}

	filtered, err := reader.Read(context.Background(), Query{ServiceID: "service", DeploymentID: "deployment", Contains: "first", Limit: 10})
	if err != nil || len(filtered.Records) != 1 || filtered.Records[0].Text != "first" {
		t.Fatalf("filtered records = %+v, err=%v", filtered, err)
	}
}

func TestReaderReadsRecentTailAndMarksTruncation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "services", "service", "deployment")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	var contents strings.Builder
	for index := range 30 {
		contents.WriteString("2026-07-12T10:00:")
		contents.WriteString(twoDigits(index))
		contents.WriteString("Z stdout F record-")
		contents.WriteString(twoDigits(index))
		contents.WriteByte('\n')
	}
	writeLog(t, filepath.Join(directory, "attempt.log"), contents.String(), time.Now())
	reader, err := NewReader(root)
	if err != nil {
		t.Fatal(err)
	}
	reader.maximumScan = 420
	window, err := reader.Read(context.Background(), Query{ServiceID: "service", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !window.Truncated || len(window.Records) != 3 || window.Records[2].Text != "record-29" {
		t.Fatalf("tail window = %+v", window)
	}
}

func TestReaderRejectsTraversalAndSymlinkSegments(t *testing.T) {
	reader, err := NewReader(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(context.Background(), Query{ServiceID: "../state", Limit: 1}); err == nil {
		t.Fatal("traversal service ID was accepted")
	}
}

func writeLog(t *testing.T, path, contents string, modified time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func twoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
