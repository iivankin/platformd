package main

import (
	"context"
	"os"

	"github.com/iivankin/platformd/internal/command"
	"github.com/iivankin/platformd/internal/containerengine"
)

func main() {
	if containerengine.InitReexec() {
		return
	}
	os.Exit(command.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
