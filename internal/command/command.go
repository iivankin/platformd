package command

import (
	"context"
	"fmt"
	"io"

	"github.com/iivankin/platformd/internal/daemon"
)

const usage = "usage: platformd init [flags]\n"

// Run dispatches the one public command and private process modes.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
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

func runInit(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}

	_, _ = io.WriteString(stderr, "platformd: init is not available in this development build\n")
	return 1
}
