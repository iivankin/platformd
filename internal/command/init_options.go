package command

import (
	"flag"
	"fmt"
	"io"
)

type initOptions struct {
	inputFD        int
	restore        bool
	rollbackUpdate bool
	installUpdate  string
	binaryPath     string
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
	flags.BoolVar(&options.restore, "restore", false, "restore an installation from its remote control backup")
	flags.BoolVar(&options.rollbackUpdate, "rollback-update", false, "restore the previous signed release before schema migration")
	flags.StringVar(&options.installUpdate, "install-signed-update", "", "install a signed forward fix from a local manifest or HTTPS URL")
	flags.StringVar(&options.binaryPath, "binary", "", "use a local binary with --install-signed-update")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = io.WriteString(stdout, usage)
			return initOptions{}, 0
		}
		return initOptions{}, 2
	}
	recoveryModes := 0
	if options.restore {
		recoveryModes++
	}
	if options.rollbackUpdate {
		recoveryModes++
	}
	if options.installUpdate != "" {
		recoveryModes++
	}
	if flags.NArg() != 0 || options.inputFD < -1 || recoveryModes > 1 ||
		(recoveryModes != 0 && !options.restore && options.inputFD != -1) ||
		(options.binaryPath != "" && options.installUpdate == "") {
		_, _ = fmt.Fprint(stderr, usage)
		return initOptions{}, 2
	}
	return options, -1
}
