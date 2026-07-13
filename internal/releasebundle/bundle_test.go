package releasebundle_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/releasebundle"
)

func TestAppendOpenVerifyAndExtract(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executable := filepath.Join(root, "platformd")
	writeFile(t, executable, append([]byte("\x7fELF"), bytes.Repeat([]byte{0x42}, 128)...), 0o755)
	runtimeDirectory := filepath.Join(root, "source")
	if err := os.MkdirAll(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runtimeDirectory, "crun"), []byte("runtime-binary"), 0o755)
	writeFile(t, filepath.Join(runtimeDirectory, "seccomp.json"), []byte("{}"), 0o600)

	if err := releasebundle.Append(executable, runtimeDirectory); err != nil {
		t.Fatal(err)
	}
	bundle, err := releasebundle.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if err := bundle.Verify(); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(root, "extracted")
	if err := bundle.Extract(extracted); err != nil {
		t.Fatal(err)
	}
	if err := bundle.VerifyExtracted(extracted); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(filepath.Join(extracted, "runtime", "crun"))
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "runtime-binary" {
		t.Fatalf("extracted crun = %q", value)
	}
	info, err := os.Stat(filepath.Join(extracted, "runtime", "crun"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("crun mode = %04o", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(extracted, "runtime", "crun"), []byte("tampered-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := bundle.VerifyExtracted(extracted); err == nil {
		t.Fatal("tampered installed runtime was accepted")
	}
	if err := releasebundle.Append(executable, runtimeDirectory); err == nil {
		t.Fatal("second bundle append succeeded")
	}
}

func TestOpenRejectsMissingAndTraversalBundle(t *testing.T) {
	t.Parallel()

	plain := filepath.Join(t.TempDir(), "plain")
	writeFile(t, plain, []byte("\x7fELFplain"), 0o755)
	if _, err := releasebundle.Open(plain); !errors.Is(err, releasebundle.ErrNoBundle) {
		t.Fatalf("plain executable error = %v", err)
	}

	malicious := filepath.Join(t.TempDir(), "malicious")
	file, err := os.OpenFile(malicious, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("\x7fELF")); err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := releasebundle.Open(malicious); err == nil {
		t.Fatal("path traversal bundle was accepted")
	}
}

func TestOpenRejectsMalformedBundleProfiles(t *testing.T) {
	t.Parallel()

	payload := []byte("runtime")
	digest := sha256.Sum256(payload)
	validManifest := `{"formatVersion":1,"files":[{"path":"runtime/helper","mode":493,"size":7,"sha256":"` +
		hex.EncodeToString(digest[:]) + `"}]}`
	valid := rawBundle(t, []rawBundleEntry{
		{name: "runtime/helper", body: payload, mode: 0o755},
		{name: "bundle-manifest.json", body: []byte(validManifest), mode: 0o600},
	})

	tests := map[string][]byte{
		"truncated": valid[:len(valid)-1],
		"trailing":  append(append([]byte(nil), valid...), 0),
		"uint overflow": rawBundle(t, []rawBundleEntry{
			{name: "runtime/helper", body: payload, mode: 0o755},
			{name: "bundle-manifest.json", mode: 0o600, body: []byte(
				`{"formatVersion":1,"files":[{"path":"runtime/helper","mode":493,"size":18446744073709551616,"sha256":"` +
					hex.EncodeToString(digest[:]) + `"}]}`,
			)},
		}),
		"duplicate JSON key": rawBundle(t, []rawBundleEntry{
			{name: "runtime/helper", body: payload, mode: 0o755},
			{name: "bundle-manifest.json", mode: 0o600, body: []byte(
				`{"formatVersion":1,"formatVersion":1,"files":[{"path":"runtime/helper","mode":493,"size":7,"sha256":"` +
					hex.EncodeToString(digest[:]) + `"}]}`,
			)},
		}),
		"duplicate archive entry": rawBundle(t, []rawBundleEntry{
			{name: "runtime/helper", body: payload, mode: 0o755},
			{name: "runtime/helper", body: payload, mode: 0o755},
			{name: "bundle-manifest.json", body: []byte(validManifest), mode: 0o600},
		}),
	}
	zip64 := append([]byte(nil), valid...)
	eocd := bytes.LastIndex(zip64, []byte{'P', 'K', 0x05, 0x06})
	if eocd < 0 {
		t.Fatal("valid fixture has no EOCD")
	}
	for index := eocd + 8; index < eocd+12; index++ {
		zip64[index] = 0xff
	}
	tests["ZIP64 sentinel"] = zip64

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "platformd")
			writeFile(t, path, value, 0o755)
			bundle, err := releasebundle.Open(path)
			if bundle != nil {
				_ = bundle.Close()
			}
			if err == nil {
				t.Fatalf("%s bundle was accepted", name)
			}
			if name == "duplicate JSON key" && !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate JSON error = %v", err)
			}
		})
	}
}

type rawBundleEntry struct {
	name string
	body []byte
	mode fs.FileMode
}

func rawBundle(t *testing.T, entries []rawBundleEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	_, _ = output.Write([]byte("\x7fELF"))
	archive := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(entry.mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeFile(t *testing.T, path string, value []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, value, mode); err != nil {
		t.Fatal(err)
	}
}
