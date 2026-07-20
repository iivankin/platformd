package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/iivankin/platformd/internal/portforwardclient"
	"github.com/iivankin/platformd/internal/version"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("platformd-forward", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	endpoint := flags.String("url", "", "WSS endpoint returned by platformd")
	localPort := flags.Int("local-port", 0, "localhost TCP port to listen on")
	showVersion := flags.Bool("version", false, "print version")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "usage: platformd-forward --url <wss-url> --local-port <port>")
		_, _ = fmt.Fprintln(flags.Output(), "The ticket is read from PLATFORMD_FORWARD_TICKET.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		_, _ = fmt.Fprintln(os.Stdout, version.Version)
		return nil
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return errors.New("unexpected positional arguments")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return portforwardclient.Run(ctx, portforwardclient.Config{
		URL: *endpoint, Ticket: os.Getenv("PLATFORMD_FORWARD_TICKET"),
		LocalPort: *localPort, Output: os.Stderr,
	})
}
