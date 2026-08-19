package command

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type joinOptions struct {
	url        string
	token      string
	name       string
	publicIPv4 string
}

func parseJoinOptions(args []string, stdout, stderr io.Writer) (joinOptions, int) {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, _ = io.WriteString(stdout, joinUsage)
		return joinOptions{}, 0
	}
	var options joinOptions
	flags := flag.NewFlagSet("platformd join", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, joinUsage) }
	flags.StringVar(&options.url, "url", "", "parent admin URL, for example https://admin.example.com")
	flags.StringVar(&options.token, "token", "", "one-time join token from the parent Settings → Servers page")
	flags.StringVar(&options.name, "name", "", "lowercase DNS label for this child server")
	flags.StringVar(&options.publicIPv4, "public-ipv4", "", "public IPv4 for Cloudflare A records; detected when omitted")
	if err := flags.Parse(args); err != nil {
		return joinOptions{}, 2
	}
	if flags.NArg() != 0 || options.url == "" || options.token == "" {
		_, _ = io.WriteString(stderr, joinUsage)
		return joinOptions{}, 2
	}
	options.url = strings.TrimRight(strings.TrimSpace(options.url), "/")
	if !strings.HasPrefix(options.url, "https://") {
		_, _ = fmt.Fprintf(stderr, "platformd: join URL must be https://\n")
		return joinOptions{}, 2
	}
	return options, -1
}
