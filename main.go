package main

import (
	"context"
	"os"

	"github.com/iivankin/platformd/internal/command"
)

func main() {
	os.Exit(command.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
