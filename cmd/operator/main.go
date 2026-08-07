package main

import (
	"fmt"
	"os"

	"github.com/FluxSeer/fluxseer-rca/internal/operatorapp"
	"github.com/FluxSeer/fluxseer-rca/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		output, err := version.ParseOutput(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if err := version.Write(os.Stdout, output); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}
	if err := operatorapp.Run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
