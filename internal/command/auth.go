package command

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/loco"
)

func authInit(args []string, stdout io.Writer, _ func(loco.Packet)) error {
	names := []string{"state", "device-name", "app-version", "os-version", "model"}
	options, err := parseOptions(args, names...)
	if err != nil {
		return errors.New("invalid auth init arguments")
	}
	for _, name := range names {
		if options[name] == "" {
			return errors.New("auth init requires all options")
		}
	}
	if !filepath.IsAbs(options["state"]) {
		return errors.New("--state must be an absolute path")
	}
	_, err = authstate.Create(options["state"], authstate.Config{
		DeviceName:  options["device-name"],
		AppVersion:  options["app-version"],
		OSVersion:   options["os-version"],
		DeviceModel: options["model"],
	})
	if err != nil {
		return errors.New("could not initialize auth state")
	}
	_, _ = io.WriteString(stdout, "initialized client-owned auth state\ncredentials: absent\n")
	return nil
}

func authInspect(args []string, stdout io.Writer, _ func(loco.Packet)) error {
	options, err := parseOptions(args, "state")
	if err != nil || options["state"] == "" {
		return errors.New("auth inspect requires --state")
	}
	if !filepath.IsAbs(options["state"]) {
		return errors.New("--state must be an absolute path")
	}
	store, err := authstate.Open(options["state"])
	if err != nil {
		return errors.New("could not inspect auth state")
	}
	state, err := store.Snapshot()
	if err != nil {
		return errors.New("could not inspect auth state")
	}
	metadataPresent := state.Identity.Metadata.Version != 0 && state.Identity.Metadata.Platform != ""
	_, _ = fmt.Fprintf(stdout, "schema_version: %d\nidentity: present (redacted)\nmetadata: %s (redacted)\ncredentials: %s\n",
		state.Version, presentAbsent(metadataPresent), presentAbsent(state.Credentials != nil))
	return nil
}

func presentAbsent(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}
