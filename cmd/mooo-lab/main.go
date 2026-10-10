// Command mooo-lab is the entry point for reproducible protocol experiments.
// Live commands require an explicitly selected owned authenticated profile.
package main

import (
	"fmt"
	"os"

	"github.com/frrad/mooo/internal/command"
	"github.com/frrad/mooo/internal/shadow"
)

// labBSONShadowModeEnv overrides the lab's default BSON shadow mode with
// "off", "log" or "panic".
const labBSONShadowModeEnv = "MOOO_BSON_SHADOW"

// labBSONShadowDumpDirEnv names an optional private directory for raw-body
// dumps of BSON shadow discrepancies.
const labBSONShadowDumpDirEnv = "MOOO_BSON_SHADOW_DUMP_DIR"

func main() {
	// Research runs crash on decoder discrepancies; operator listing only
	// logs them. $MOOO_BSON_SHADOW overrides the mode (off, log or panic).
	mode, err := labShadowMode(os.Args[1:], os.Getenv(labBSONShadowModeEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	observer := shadow.NewObserver(shadow.Config{Mode: mode, DumpDir: os.Getenv(labBSONShadowDumpDirEnv)})
	code := command.Run(os.Args[1:], os.Stdout, os.Stderr, observer)
	if code != 0 {
		os.Exit(code)
	}
}

func labShadowMode(args []string, configured string) (shadow.Mode, error) {
	if configured != "" {
		return shadow.ParseMode(configured)
	}
	if command.IsLive(args) {
		return shadow.ModeLog, nil
	}
	return shadow.ModePanic, nil
}
