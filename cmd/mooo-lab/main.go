// Command mooo-lab is the entry point for reproducible protocol experiments.
// Live commands require an explicitly selected owned authenticated profile.
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
	// Research runs crash on decoder discrepancies; operator listing reports
	// them with the same log-only default as the bridge.
	// $MOOO_BSON_SHADOW overrides the mode (off, log or panic).
	mode, err := labShadowMode(os.Args[1:], os.Getenv(client.BSONShadowModeEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	client.SetBSONShadow(client.BSONShadowConfig{Mode: mode, DumpDir: os.Getenv(labBSONShadowDumpDirEnv)})
	code := command.Run(os.Args[1:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func labShadowMode(args []string, configured string) (client.BSONShadowMode, error) {
	mode, err := client.ParseBSONShadowMode(configured)
	if err != nil {
		return mode, err
	}
	if mode == client.BSONShadowDefault {
		mode = client.BSONShadowPanic
		if len(args) >= 2 && args[0] == "chats" && args[1] == "list" {
			mode = client.BSONShadowLog
		}
	}
	return mode, nil
}
