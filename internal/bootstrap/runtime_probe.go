package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/releasebundle"
)

const (
	runtimeProbeTimeout     = 5 * time.Second
	maximumProbeOutputBytes = 8 << 10
)

func probeReleaseRuntime(slot string) error {
	helpers, err := releasebundle.RuntimeHelperPaths(slot)
	if err != nil {
		return err
	}
	for _, helper := range helpers {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeProbeTimeout)
		output := &boundedProbeOutput{}
		command := exec.CommandContext(ctx, helper, "--version")
		// The probe must exercise the host's default loader configuration, not
		// caller-controlled LD_PRELOAD or LD_LIBRARY_PATH values from init.
		command.Env = []string{"LC_ALL=C", "LANG=C"}
		command.Stdout = output
		command.Stderr = output
		runErr := command.Run()
		contextErr := ctx.Err()
		cancel()
		if runErr == nil {
			continue
		}
		name := filepath.Base(helper)
		if errors.Is(contextErr, context.DeadlineExceeded) {
			return fmt.Errorf("runtime helper %s version probe timed out", name)
		}
		diagnostic := strings.TrimSpace(output.String())
		if diagnostic == "" {
			return fmt.Errorf("runtime helper %s is incompatible with this host: %w", name, runErr)
		}
		return fmt.Errorf("runtime helper %s is incompatible with this host: %w: %s", name, runErr, diagnostic)
	}
	return nil
}

type boundedProbeOutput struct {
	buffer bytes.Buffer
}

func (output *boundedProbeOutput) Write(value []byte) (int, error) {
	written := len(value)
	remaining := maximumProbeOutputBytes - output.buffer.Len()
	if remaining > 0 {
		_, _ = output.buffer.Write(value[:min(remaining, len(value))])
	}
	return written, nil
}

func (output *boundedProbeOutput) String() string {
	return output.buffer.String()
}
