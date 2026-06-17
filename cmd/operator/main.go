package main

import (
	"fmt"
	"os"

	"fluxagent/internal/operatorapp"
)

func main() {
	if err := operatorapp.Run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
