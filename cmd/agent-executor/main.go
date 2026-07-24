package main

import (
	"fmt"
	"os"

	"fluxagent/internal/agentexecutor"
	"fluxagent/internal/version"
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
	if err := agentexecutor.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
