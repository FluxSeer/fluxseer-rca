package cli

import (
	"fmt"
	"io"

	"github.com/FluxSeer/fluxseer-rca/internal/version"
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
	case "report":
		return runReport(args[1:], stdout, stderr)
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
		return fmt.Errorf("unknown fluxseer subcommand %q", args[0])
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  fluxseer demo")
	_, _ = fmt.Fprintln(w, "  fluxseer investigate <kind> <name> [flags]")
	_, _ = fmt.Fprintln(w, "  fluxseer report riskrule <name> [flags]")
	_, _ = fmt.Fprintln(w, "  fluxseer report agentaction <name> [flags]")
	_, _ = fmt.Fprintln(w, "  fluxseer version [--output=json]")
}
