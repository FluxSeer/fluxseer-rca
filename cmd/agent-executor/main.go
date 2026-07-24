package main

import (
	"fmt"
	"os"

	"fluxagent/internal/agentexecutor"
)

func main() {
	if err := agentexecutor.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
