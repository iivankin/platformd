package command_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/command"
)

func TestOnlyInitIsExposedAsPublicCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := command.Run(context.Background(), []string{"deploy"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q, want unknown command error", stderr.String())
	}
	if strings.Contains(stderr.String(), "__daemon") {
		t.Fatalf("private mode leaked in public usage: %q", stderr.String())
	}
}

func TestInitHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := command.Run(context.Background(), []string{"init", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != "usage: platformd init [--input-fd <fd>] [--restore | --rollback-update | --install-signed-update <manifest> [--binary <path>]]\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestInitRecoveryModesAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{"init", "--binary", "/tmp/platformd"},
		{"init", "--rollback-update", "--install-signed-update", "/tmp/manifest"},
		{"init", "--input-fd", "3", "--install-signed-update", "/tmp/manifest"},
		{"init", "--restore", "--rollback-update"},
		{"init", "--restore", "--install-signed-update", "/tmp/manifest"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := command.Run(context.Background(), arguments, &stdout, &stderr); code != 2 {
			t.Fatalf("arguments %v exit = %d, stderr=%q", arguments, code, stderr.String())
		}
	}
}
