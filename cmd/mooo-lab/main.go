// Command mooo-lab is the entry point for reproducible protocol experiments.
// It intentionally has no account or network behavior yet.
package main

import (
	"fmt"
	"os"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/command"
)

// labBSONShadowDumpDirEnv names an optional private directory for raw-body
// dumps of BSON shadow discrepancies.
const labBSONShadowDumpDirEnv = "MOOO_BSON_SHADOW_DUMP_DIR"

func main() {
	// Lab runs crash on any decoder discrepancy so it cannot go unnoticed.
	// $MOOO_BSON_SHADOW overrides the mode (off, log or panic).
	mode, err := client.ParseBSONShadowMode(os.Getenv(client.BSONShadowModeEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if mode == client.BSONShadowDefault {
		mode = client.BSONShadowPanic
	}
	client.SetBSONShadow(client.BSONShadowConfig{Mode: mode, DumpDir: os.Getenv(labBSONShadowDumpDirEnv)})
	code := command.Run(os.Args[1:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}
