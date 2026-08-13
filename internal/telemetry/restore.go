package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func RestoreBackup(ctx context.Context, binary, archive, volume string) error {
	if ctx == nil || !safeAbsolutePath(binary) || !safeAbsolutePath(archive) || !safeAbsolutePath(volume) {
		return errors.New("telemetry restore configuration is incomplete")
	}
	command := exec.CommandContext(ctx, binary, "--restore-backup", archive, volume)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("restore embedded telemetry: %w", err)
	}
	return nil
}

func safeAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator)
}
