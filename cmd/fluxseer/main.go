package main

import (
	"fmt"
	"os"

	"github.com/FluxSeer/fluxseer-rca/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
