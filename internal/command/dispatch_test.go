package command

import (
	"strings"
	"testing"
)

const goldenUsage = `usage:
  mooo-lab contacts photo --state ABSOLUTE_PATH --chat CHAT_ID --user USER_ID --output ABSOLUTE_PATH
  mooo-lab chats list --state ABSOLUTE_PATH --output ABSOLUTE_PATH
  mooo-lab auth init --state ABSOLUTE_PATH --device-name NAME --app-version VERSION --os-version VERSION --model MODEL
  mooo-lab auth inspect --state ABSOLUTE_PATH
`

const invalidCommand = "invalid command; run `mooo-lab --help` for usage\n"

// TestDispatchGolden pins the exact exit code and output of every dispatch
// path, including help placement, so refactoring the command table cannot
// change what an operator sees.
func TestDispatchGolden(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{args: []string{"--help"}, code: 0, stdout: goldenUsage},
		{args: []string{"-h"}, code: 0, stdout: goldenUsage},
		{args: []string{"--help", "extra"}, code: 0, stdout: goldenUsage},
		{args: []string{"auth", "--help"}, code: 0, stdout: goldenUsage},
		{args: []string{"auth", "-h"}, code: 0, stdout: goldenUsage},
		{args: []string{"auth", "init", "--help"}, code: 0, stdout: goldenUsage},
		{args: []string{"auth", "inspect", "-h"}, code: 0, stdout: goldenUsage},
		{args: []string{"auth", "unknown", "--help"}, code: 0, stdout: goldenUsage},
		{args: []string{"auth"}, code: 2, stderr: invalidCommand},
		{args: []string{"auth", "init"}, code: 2, stderr: invalidCommand},
		{args: []string{"auth", "inspect"}, code: 2, stderr: invalidCommand},
		{args: []string{"auth", "unknown", "--state", "/x"}, code: 2, stderr: invalidCommand},
		{args: []string{"auth", "init", "--state", "/x", "--help"}, code: 2, stderr: "error: invalid auth init arguments\n"},
		{args: []string{"auth", "inspect", "--state"}, code: 2, stderr: "error: auth inspect requires --state\n"},
		{args: []string{"chats"}, code: 2, stderr: invalidCommand},
		{args: []string{"chats", "--help"}, code: 2, stderr: invalidCommand},
		{args: []string{"chats", "unknown"}, code: 2, stderr: invalidCommand},
		{args: []string{"chats", "list"}, code: 2, stderr: "error: chats list requires absolute --state and --output\n"},
		{args: []string{"chats", "list", "--help"}, code: 2, stderr: "error: chats list requires absolute --state and --output\n"},
		{args: []string{"contacts"}, code: 2, stderr: invalidCommand},
		{args: []string{"contacts", "photo", "-h"}, code: 2, stderr: "error: contacts photo requires absolute --state and --output and positive --chat and --user\n"},
		{args: []string{"unexpected"}, code: 2, stderr: invalidCommand},
		{args: []string{"list", "chats"}, code: 2, stderr: invalidCommand},
	} {
		code, stdout, stderr := invoke(tc.args...)
		if code != tc.code || stdout != tc.stdout || stderr != tc.stderr {
			t.Errorf("args=%q: code=%d stdout=%q stderr=%q, want code=%d stdout=%q stderr=%q",
				tc.args, code, stdout, stderr, tc.code, tc.stdout, tc.stderr)
		}
	}
}

// TestOptionGrammarGolden pins which option spellings are accepted. Only
// --name value and --name=value are valid; the separate value may not start
// with a dash, duplicates and empty values are rejected, and no rejected
// token is ever echoed.
func TestOptionGrammarGolden(t *testing.T) {
	const secret = "synthetic-secret-value"
	// auth init distinguishes a grammar error from a missing option.
	rejected := "error: invalid auth init arguments\n"
	rest := []string{"--device-name", "n", "--app-version", "a", "--os-version", "o", "--model", "m"}
	for _, args := range [][]string{
		{"-state", "/" + secret},
		{"-state=/" + secret},
		{"--state", "-" + secret},
		{"--state", "--" + secret},
		{"--state", ""},
		{"--state="},
		{"--state=/" + secret, "--state=/" + secret},
		{"--state", "/" + secret, "--state", "/" + secret},
		{"--state", "/" + secret, "--"},
		{"--", "--state", "/" + secret},
		{"--state", "/" + secret, secret},
		{secret, "--state", "/" + secret},
		{"--" + secret, "/x"},
		{"--" + secret + "=/x"},
		{"---state", "/x"},
		{"--=" + secret},
		{"--state", "/" + secret, "--help"},
		{"-"},
	} {
		code, stdout, stderr := invoke(append(append([]string{"auth", "init"}, args...), rest...)...)
		if code != 2 || stdout != "" || stderr != rejected || strings.Contains(stderr, secret) {
			t.Errorf("args=%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	// Both accepted spellings reach authstate; a value may contain "=" or
	// start with a dash in the joined form.
	for _, args := range [][]string{
		{"--state", "/nonexistent/" + secret},
		{"--state=/nonexistent/" + secret},
		{"--state=/nonexistent/a=" + secret},
	} {
		code, stdout, stderr := invoke(append([]string{"auth", "inspect"}, args...)...)
		if code != 2 || stdout != "" || stderr != "error: could not inspect auth state\n" {
			t.Errorf("args=%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	code, stdout, stderr := invoke("auth", "inspect", "--state=-"+secret)
	if code != 2 || stdout != "" || stderr != "error: --state must be an absolute path\n" {
		t.Errorf("dash value in joined form: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestIsLiveNamesOnlyProfileCommands(t *testing.T) {
	for _, tc := range []struct {
		args []string
		live bool
	}{
		{args: []string{"chats", "list"}, live: true},
		{args: []string{"chats", "list", "--state", "/x"}, live: true},
		{args: []string{"contacts", "photo"}, live: true},
		{args: []string{"auth", "init", "--state", "/x"}, live: false},
		{args: []string{"auth", "inspect", "--state", "/x"}, live: false},
		{args: []string{"chats"}, live: false},
		{args: []string{"list", "chats"}, live: false},
		{args: nil, live: false},
	} {
		if got := IsLive(tc.args); got != tc.live {
			t.Errorf("IsLive(%q) = %v, want %v", tc.args, got, tc.live)
		}
	}
}

func TestLiveCommandOptionsAreNotEchoed(t *testing.T) {
	const secret = "synthetic-secret-value"
	for _, args := range [][]string{
		{"chats", "list", "--state", "/" + secret, "--bogus", secret},
		{"chats", "list", "--bogus=" + secret},
		{"contacts", "photo", "--chat", secret, "--user", secret, "--state", "/" + secret, "--output", "/" + secret},
		{"contacts", "photo", "--" + secret},
	} {
		code, stdout, stderr := invoke(args...)
		if code != 2 || stdout != "" || strings.Contains(stderr, secret) || !strings.HasPrefix(stderr, "error: ") {
			t.Errorf("args=%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}
