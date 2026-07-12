package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/iivankin/platformd/internal/releasebundle"
)

func main() {
	executable := flag.String("executable", "", "Linux platformd executable to append to")
	runtimeDirectory := flag.String("runtime", "", "runtime payload directory")
	flag.Parse()
	if *executable == "" || *runtimeDirectory == "" || flag.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: go run ./tools/bundle --executable <path> --runtime <directory>")
		os.Exit(2)
	}
	if err := releasebundle.ValidateExecutable(*executable); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bundle: %v\n", err)
		os.Exit(1)
	}
	if err := releasebundle.Append(*executable, *runtimeDirectory); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bundle: %v\n", err)
		os.Exit(1)
	}
}
