package command

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errInvalidOptions = errors.New("invalid options")

// parseOptions accepts only --name value and --name=value forms of the named
// options. A separate value may not start with a dash; positional arguments,
// duplicates and empty values are rejected. Errors never carry the offending
// token and the flag set's own diagnostics are discarded, so no value is ever
// echoed.
func parseOptions(args []string, names ...string) (map[string]string, error) {
	// flag also accepts -name and a dash-led separate value; reject both.
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") || len(args[i]) == 2 {
			return nil, errInvalidOptions
		}
		if !strings.Contains(args[i], "=") && i+1 < len(args) {
			i++
			if strings.HasPrefix(args[i], "-") {
				return nil, errInvalidOptions
			}
		}
	}
	values := make(map[string]string, len(names))
	set := flag.NewFlagSet("mooo-lab", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Usage = func() {}
	for _, name := range names {
		set.Var(onceValue{name: name, values: values}, name, "")
	}
	if set.Parse(args) != nil || set.NArg() != 0 {
		return nil, errInvalidOptions
	}
	return values, nil
}

// onceValue stores one non-empty option value and rejects a repeat.
type onceValue struct {
	name   string
	values map[string]string
}

func (v onceValue) String() string { return "" }

func (v onceValue) Set(value string) error {
	if _, duplicate := v.values[v.name]; duplicate || value == "" {
		return errInvalidOptions
	}
	v.values[v.name] = value
	return nil
}

// reservePrivateOutput exclusively creates path as a 0600 file in an existing
// directory only its owner can access. Commands call it before opening a
// profile so an unsafe or existing destination never reaches authentication.
// commit syncs, closes and keeps the file; abort removes it unless commit
// succeeded. kind names the output in error messages.
func reservePrivateOutput(path, kind string) (file *os.File, commit func() error, abort func(), err error) {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, nil, nil, errors.New(kind + " output requires a private directory")
	}
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, nil, nil, errors.New("could not reserve " + kind + " output")
	}
	kept := false
	commit = func() error {
		if err := file.Sync(); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		kept = true
		return nil
	}
	abort = func() {
		if !kept {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}
	return file, commit, abort, nil
}
