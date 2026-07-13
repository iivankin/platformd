package masterkey_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/masterkey"
)

func TestLoadOrCreateReusesExactKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "master.key")
	first, created, err := masterkey.LoadOrCreate(path, os.Geteuid(), bytes.NewReader(bytes.Repeat([]byte{0x2a}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first call did not report creation")
	}
	second, created, err := masterkey.LoadOrCreate(path, os.Geteuid(), bytes.NewReader(bytes.Repeat([]byte{0x7f}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing key reported as newly created")
	}
	if first != second {
		t.Fatal("existing key was replaced")
	}
	if got := masterkey.RecoveryString(first); got != "KioqKioqKioqKioqKioqKioqKioqKioqKioqKioqKio" {
		t.Fatalf("recovery string = %q", got)
	}
}

func TestLoadOrCreateRejectsUnsafeExistingPath(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "master.key")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := masterkey.LoadOrCreate(symlink, os.Geteuid(), bytes.NewReader(bytes.Repeat([]byte{2}, 32))); err == nil {
		t.Fatal("symlink master key was accepted")
	}

	unsafeMode := filepath.Join(directory, "unsafe.key")
	if err := os.WriteFile(unsafeMode, bytes.Repeat([]byte{1}, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := masterkey.LoadOrCreate(unsafeMode, os.Geteuid(), bytes.NewReader(bytes.Repeat([]byte{2}, 32))); err == nil {
		t.Fatal("world-readable master key was accepted")
	}
}

func TestPartialExistingKeyIsNeverRegenerated(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, created, err := masterkey.LoadOrCreate(path, os.Geteuid(), bytes.NewReader(bytes.Repeat([]byte{2}, 32))); err == nil || created {
		t.Fatalf("partial key result = created %v, error %v", created, err)
	}
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "partial" {
		t.Fatalf("partial key was modified: %q", value)
	}
}

func TestRecoveryStringRoundTripAndInstallRefusesReplacement(t *testing.T) {
	t.Parallel()
	key, err := masterkey.ParseRecoveryString("KioqKioqKioqKioqKioqKioqKioqKioqKioqKioqKio")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config", "master.key")
	if err := masterkey.Install(path, os.Geteuid(), key); err != nil {
		t.Fatal(err)
	}
	if err := masterkey.Install(path, os.Geteuid(), key); err != nil {
		t.Fatalf("same-key retry failed: %v", err)
	}
	different, err := masterkey.ParseRecoveryString("KysrKysrKysrKysrKysrKysrKysrKysrKysrKysrKys")
	if err != nil {
		t.Fatal(err)
	}
	if err := masterkey.Install(path, os.Geteuid(), different); err == nil {
		t.Fatal("existing master key was replaceable")
	}
}
