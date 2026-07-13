package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/daemon"
	"github.com/iivankin/platformd/internal/disasterrestore"
)

const usage = "usage: platformd init [--input-fd <fd>] [--restore | --rollback-update | --install-signed-update <manifest> [--binary <path>]]\n"

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
	case "__restore-import":
		return runRestoreImport(ctx, args[1:], stdout, stderr)
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
	if options.restore {
		provider, err := restoreInputProvider(options.inputFD)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "platformd: %v\n", err)
			return 1
		}
		restorer, err := disasterrestore.ProductionRestorer(provider)
		if err == nil {
			err = restorer.Restore(ctx)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "platformd: restore: %v\n", err)
			return 1
		}
		_, _ = io.WriteString(stdout, "platformd restored\n")
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

func runRestoreImport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 || os.Geteuid() != 0 {
		_, _ = io.WriteString(stderr, "platformd: invalid private restore importer invocation\n")
		return 2
	}
	file := os.NewFile(3, "platformd-restore-import")
	if file == nil {
		_, _ = io.WriteString(stderr, "platformd: restore importer input fd is unavailable\n")
		return 1
	}
	defer file.Close()
	payload, err := disasterrestore.ReadImportPayload(file)
	if err == nil {
		var result disasterrestore.ImportResult
		result, err = disasterrestore.ImportSnapshot(ctx, payload)
		if err == nil {
			err = json.NewEncoder(stdout).Encode(result)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "platformd: restore importer: %v\n", err)
		return 1
	}
	return 0
}
