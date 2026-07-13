package command

import (
	"flag"
	"fmt"
	"io"
)

type initOptions struct {
	inputFD        int
	rollbackUpdate bool
}

func parseInitOptions(args []string, stdout, stderr io.Writer) (initOptions, int) {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, _ = io.WriteString(stdout, usage)
		return initOptions{}, 0
	}
	options := initOptions{inputFD: -1}
	flags := flag.NewFlagSet("platformd init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = io.WriteString(stderr, usage) }
	flags.IntVar(&options.inputFD, "input-fd", -1, "read bounded bootstrap JSON from an inherited file descriptor")
	flags.BoolVar(&options.rollbackUpdate, "rollback-update", false, "restore the previous signed release before schema migration")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = io.WriteString(stdout, usage)
			return initOptions{}, 0
		}
		return initOptions{}, 2
	}
	if flags.NArg() != 0 || options.inputFD < -1 || (options.rollbackUpdate && options.inputFD != -1) {
		_, _ = fmt.Fprint(stderr, usage)
		return initOptions{}, 2
	}
	return options, -1
}
