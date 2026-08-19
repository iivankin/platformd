//go:build platformd_worker

package command

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/iivankin/platformd/internal/daemon"
)

const usage = "usage: platformd join --url https://admin.example.com --token pjn_... [--public-ipv4 <ipv4>] [--name <label>]\n"

// Run dispatches the public join command and the private worker process mode.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}

	switch args[0] {
	case "join":
		return runJoin(ctx, args[1:], stdout, stderr)
	case "__worker":
		runtime.LockOSThread()
		if err := daemon.RunWorker(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "platformd: %v\n", err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "platformd: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
