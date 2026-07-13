package command

import (
	"context"
	"fmt"
	"io"

	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/daemon"
)

const usage = "usage: platformd init [--input-fd <fd> | --rollback-update | --install-signed-update <manifest> [--binary <path>]]\n"

// Run dispatches the one public command and private process modes.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}

	switch args[0] {
	case "init":
		return runInit(ctx, args[1:], stdout, stderr)
	case "__daemon":
		if err := daemon.Run(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "platformd: %v\n", err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "platformd: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code := parseInitOptions(args, stdout, stderr)
	if code != -1 {
		return code
	}
	if options.rollbackUpdate {
		rollback, err := bootstrap.ProductionUpdateRollback()
		if err == nil {
			err = rollback.Run(ctx)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "platformd: rollback update: %v\n", err)
			return 1
		}
		_, _ = io.WriteString(stdout, "platformd update rolled back\n")
		return 0
	}
	if options.installUpdate != "" {
		installer, err := bootstrap.ProductionSignedUpdateInstaller(options.installUpdate, options.binaryPath)
		if err == nil {
			err = installer.Run(ctx)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "platformd: install signed update: %v\n", err)
			return 1
		}
		_, _ = io.WriteString(stdout, "platformd signed update installed\n")
		return 0
	}
	provider, err := bootstrapInputProvider(options.inputFD)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "platformd: %v\n", err)
		return 1
	}
	installer := bootstrap.ProductionInstaller(confirmRecoveryKey, provider)
	if err := installer.Init(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "platformd: init: %v\n", err)
		return 1
	}
	_, _ = io.WriteString(stdout, "platformd initialized\n")
	return 0
}
