// Command mooo-lab is the entry point for reproducible protocol experiments.
// It intentionally has no account or network behavior yet.
package main

import (
	"fmt"

	"github.com/frrad/mooo/internal/buildinfo"
)

func main() {
	fmt.Printf("mooo-lab %s\n", buildinfo.Version)
}
