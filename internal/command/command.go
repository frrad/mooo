// Package command implements the deliberately small mooo-lab CLI.
// It owns argument validation and presentation while authstate owns persistence
// and validation. Explicit chat listing uses one selected authenticated profile.
package command

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/frrad/mooo/internal/buildinfo"
	"github.com/frrad/mooo/internal/protocol/loco"
)

const usage = `usage:
  mooo-lab contacts photo --state ABSOLUTE_PATH --chat CHAT_ID --user USER_ID --output ABSOLUTE_PATH
  mooo-lab chats list --state ABSOLUTE_PATH --output ABSOLUTE_PATH
  mooo-lab auth init --state ABSOLUTE_PATH --device-name NAME --app-version VERSION --os-version VERSION --model MODEL
  mooo-lab auth inspect --state ABSOLUTE_PATH
`

// handler runs one subcommand on the arguments after its two-word name. A
// returned error's message is printed as-is, so it must never contain a flag
// value or filesystem path.
type handler struct {
	// live marks a command that opens an authenticated profile and talks to
	// Kakao, rather than only touching local auth state.
	live bool
	run  func(args []string, stdout io.Writer, packetObserver func(loco.Packet)) error
}

var handlers = map[string]handler{
	"auth init":      {run: authInit},
	"auth inspect":   {run: authInspect},
	"chats list":     {live: true, run: listChats},
	"contacts photo": {live: true, run: contactPhoto},
}

// Run executes one CLI invocation and returns a process-style exit code. It
// never includes flag values or filesystem paths in its output. A non-nil
// packetObserver sees every LOCO packet the opened client reads.
func Run(args []string, stdout, stderr io.Writer, packetObserver func(loco.Packet)) int {
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
	if wantsHelp(args) {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	h, ok := lookup(args)
	if !ok {
		_, _ = io.WriteString(stderr, "invalid command; run `mooo-lab --help` for usage\n")
		return 2
	}
	if err := h.run(args[2:], stdout, packetObserver); err != nil {
		_, _ = io.WriteString(stderr, "error: "+err.Error()+"\n")
		return 2
	}
	return 0
}

// IsLive reports whether args name a command that opens an authenticated
// profile. mooo-lab uses it to pick the default BSON shadow mode.
func IsLive(args []string) bool {
	h, ok := lookup(args)
	return ok && h.live
}

// wantsHelp reports a help request: first, after the auth group, or right
// after an auth subcommand name. Live commands reject a later --help as an
// invalid option.
func wantsHelp(args []string) bool {
	if isHelp(args[0]) {
		return true
	}
	return args[0] == "auth" && ((len(args) >= 2 && isHelp(args[1])) || (len(args) >= 3 && isHelp(args[2])))
}

func isHelp(arg string) bool { return arg == "--help" || arg == "-h" }

// lookup finds the handler named by the first two arguments. An auth
// subcommand without options is a usage error rather than an option error.
func lookup(args []string) (handler, bool) {
	if len(args) < 2 || (args[0] == "auth" && len(args) < 3) {
		return handler{}, false
	}
	h, ok := handlers[args[0]+" "+args[1]]
	return h, ok
}

// profileShutdownTimeout bounds how long a command waits for an opened
// profile's in-flight connect and session work to join before giving up. A
// timed-out shutdown leaves the profile lease held and reports cleanup failure.
const profileShutdownTimeout = 10 * time.Second

// shutdownProfile shuts an opened profile client down on its own deadline, so
// cleanup still runs after the command's operation context has expired.
func shutdownProfile(c interface{ Shutdown(context.Context) error }) error {
	ctx, cancel := context.WithTimeout(context.Background(), profileShutdownTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}
