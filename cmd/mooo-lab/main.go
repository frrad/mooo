// Command mooo-lab is the entry point for reproducible protocol experiments.
// It intentionally has no account or network behavior yet.
package main

import (
	"os"

	"github.com/frrad/mooo/internal/command"
)

func main() {
	code := command.Run(os.Args[1:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}
