// Package command implements the deliberately small, offline mooo-lab CLI.
// It owns argument validation and presentation while authstate owns persistence
// and validation. No command in this package performs network operations.
package command

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/buildinfo"
)

const usage = `usage:
  mooo-lab auth init --state ABSOLUTE_PATH --device-name NAME --app-version VERSION --os-version VERSION --model MODEL
  mooo-lab auth inspect --state ABSOLUTE_PATH
`

// Run executes one CLI invocation and returns a process-style exit code. It
// never includes flag values or filesystem paths in its output.
func Run(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stdout, "mooo-lab %s\n", buildinfo.Version)
		return 0
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	if args[0] != "auth" || len(args) < 2 {
		return usageError(stderr)
	}
	if args[1] == "--help" || args[1] == "-h" {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	if len(args) < 3 {
		return usageError(stderr)
	}
	if args[2] == "--help" || args[2] == "-h" {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	switch args[1] {
	case "init":
		return runInit(args[2:], stdout, stderr)
	case "inspect":
		return runInspect(args[2:], stdout, stderr)
	default:
		return usageError(stderr)
	}
}

func runInit(args []string, stdout, stderr io.Writer) int {
	options, err := parseOptions(args, map[string]struct{}{
		"state": {}, "device-name": {}, "app-version": {}, "os-version": {}, "model": {},
	})
	if err != nil {
		return commandError(stderr, "invalid auth init arguments")
	}
	for _, name := range []string{"state", "device-name", "app-version", "os-version", "model"} {
		if options[name] == "" {
			return commandError(stderr, "auth init requires all options")
		}
	}
	if !filepath.IsAbs(options["state"]) {
		return commandError(stderr, "--state must be an absolute path")
	}
	_, err = authstate.Create(options["state"], authstate.Config{
		DeviceName:  options["device-name"],
		AppVersion:  options["app-version"],
		OSVersion:   options["os-version"],
		DeviceModel: options["model"],
	})
	if err != nil {
		return commandError(stderr, "could not initialize auth state")
	}
	_, _ = io.WriteString(stdout, "initialized client-owned auth state\ncredentials: absent\n")
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	options, err := parseOptions(args, map[string]struct{}{"state": {}})
	if err != nil || options["state"] == "" {
		return commandError(stderr, "auth inspect requires --state")
	}
	if !filepath.IsAbs(options["state"]) {
		return commandError(stderr, "--state must be an absolute path")
	}
	store, err := authstate.Open(options["state"])
	if err != nil {
		return commandError(stderr, "could not inspect auth state")
	}
	state, err := store.Snapshot()
	if err != nil {
		return commandError(stderr, "could not inspect auth state")
	}
	metadataPresent := state.Identity.Metadata.Version != 0 && state.Identity.Metadata.Platform != ""
	_, _ = fmt.Fprintf(stdout, "schema_version: %d\nidentity: present (redacted)\nmetadata: %s (redacted)\ncredentials: %s\n",
		state.Version, presentAbsent(metadataPresent), presentAbsent(state.Credentials != nil))
	return 0
}

func presentAbsent(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func usageError(stderr io.Writer) int {
	_, _ = io.WriteString(stderr, "invalid command; run `mooo-lab --help` for usage\n")
	return 2
}

func commandError(stderr io.Writer, message string) int {
	_, _ = io.WriteString(stderr, "error: "+message+"\n")
	return 2
}

// parseOptions accepts only --name value and --name=value forms. Values are
// retained in memory only for the immediate authstate call and are never part
// of parser errors or command output.
func parseOptions(args []string, allowed map[string]struct{}) (map[string]string, error) {
	values := make(map[string]string, len(allowed))
	for i := 0; i < len(args); i++ {
		token := args[i]
		if !strings.HasPrefix(token, "--") || len(token) == 2 {
			return nil, fmt.Errorf("invalid option")
		}
		nameValue := strings.TrimPrefix(token, "--")
		name, value, hasEquals := strings.Cut(nameValue, "=")
		if _, ok := allowed[name]; !ok || name == "" {
			return nil, fmt.Errorf("invalid option")
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("duplicate option")
		}
		if !hasEquals {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, fmt.Errorf("missing option value")
			}
			i++
			value = args[i]
		}
		if value == "" {
			return nil, fmt.Errorf("empty option value")
		}
		values[name] = value
	}
	return values, nil
}
