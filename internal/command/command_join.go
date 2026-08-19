package command

import (
	"context"
	"fmt"
	"io"

	"github.com/iivankin/platformd/internal/bootstrap"
)

const joinUsage = "usage: platformd join --url https://admin.example.com --token pjn_... [--public-ipv4 <ipv4>] [--name <label>]\n"

func runJoin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code := parseJoinOptions(args, stdout, stderr)
	if code != -1 {
		return code
	}
	joiner := bootstrap.ProductionJoiner(options.url, options.token, options.name, options.publicIPv4)
	if err := joiner.Join(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "platformd: join: %v\n", err)
		return 1
	}
	_, _ = io.WriteString(stdout, "platformd joined the parent installation\n")
	return 0
}
