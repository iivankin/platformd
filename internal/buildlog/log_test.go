package buildlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendCapsPersistentBuildOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service", "deployment", "build.log")
	writer, err := OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), MaxBytes+1024)
	if written, err := writer.Write(payload); err != nil || written != len(payload) {
		t.Fatalf("write = %d, %v", written, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, "ignored"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != MaxBytes || !bytes.HasSuffix(content, []byte(TruncationMarker)) {
		t.Fatalf("capped log = %d bytes, suffix %q", len(content), content[len(content)-len(TruncationMarker):])
	}
}

func TestAppendMarksExactBuildLogBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service", "deployment", "build.log")
	writer, err := OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	dataLimit := MaxBytes - len(TruncationMarker)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), dataLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("overflow")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != MaxBytes || !bytes.HasSuffix(content, []byte(TruncationMarker)) {
		t.Fatalf("exact-boundary log = %d bytes", len(content))
	}
}
