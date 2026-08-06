package cli

import (
	"fmt"
	"io"

	"fluxseer/internal/version"
)

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runDemo(stdout)
	}

	switch args[0] {
	case "demo":
		return runDemo(stdout)
	case "investigate":
		return runInvestigate(args[1:], stdout, stderr)
	case "version":
		output, err := version.ParseOutput(args[1:])
		if err != nil {
			return err
		}
		return version.Write(stdout, output)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown fluxagent subcommand %q", args[0])
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  fluxagent demo")
	_, _ = fmt.Fprintln(w, "  fluxagent investigate <kind> <name> [flags]")
	_, _ = fmt.Fprintln(w, "  fluxagent version [--output=json]")
}
