package command

import (
	"bytes"
	"strings"
	"testing"
)

func invoke(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func initArgs(path string) []string {
	return []string{
		"auth", "init",
		"--state", path,
		"--device-name", "Mooo Lab Mac",
		"--app-version", "26.8.0-test",
		"--os-version", "macOS 26.0-test",
		"--model", "MacBookAir-test",
	}
}

func TestAuthInitAndInspectAreRedacted(t *testing.T) {
	path := t.TempDir() + "/client/authstate.json"
	code, stdout, stderr := invoke(initArgs(path)...)
	if code != 0 {
		t.Fatalf("init code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "initialized client-owned auth state") || !strings.Contains(stdout, "credentials: absent") {
		t.Fatalf("unexpected init output: %q", stdout)
	}
	if strings.Contains(stdout, path) || strings.Contains(stdout, "Mooo Lab Mac") {
		t.Fatal("init output revealed path or identity value")
	}
	if stderr != "" {
		t.Fatalf("init stderr = %q", stderr)
	}

	code, stdout, stderr = invoke("auth", "inspect", "--state", path)
	if code != 0 {
		t.Fatalf("inspect code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	want := "schema_version: 1\nidentity: present (redacted)\nmetadata: present (redacted)\ncredentials: absent\n"
	if stdout != want {
		t.Fatalf("inspect output = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, path) || strings.Contains(stdout, "Mooo Lab Mac") || strings.Contains(stdout, "26.8.0") {
		t.Fatal("inspect output revealed a sensitive value")
	}
	if stderr != "" {
		t.Fatalf("inspect stderr = %q", stderr)
	}
}

func TestAuthInitRequiresAbsoluteStateAndAllFields(t *testing.T) {
	base := []string{"auth", "init", "--state", "relative/state.json", "--device-name", "name", "--app-version", "app", "--os-version", "os", "--model", "model"}
	code, _, stderr := invoke(base...)
	if code != 2 || !strings.Contains(stderr, "absolute path") {
		t.Fatalf("relative state: code=%d stderr=%q", code, stderr)
	}

	code, _, stderr = invoke("auth", "init", "--state", "/tmp/not-created/state.json")
	if code != 2 || !strings.Contains(stderr, "requires all options") {
		t.Fatalf("missing fields: code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "/tmp/not-created") {
		t.Fatal("error revealed sensitive state path")
	}
}

func TestUnknownAndMalformedArgumentsAreRejectedWithoutEcho(t *testing.T) {
	secret := "/private/synthetic-token"
	for _, args := range [][]string{
		{"auth", "init", "--state", secret, "--unknown", "secret-value"},
		{"auth", "inspect", "--state", secret, "positional-secret"},
		{"auth", "inspect", "--state=" + secret, "--state", secret},
		{"auth", "inspect", "-state", secret},
	} {
		code, stdout, stderr := invoke(args...)
		if code != 2 {
			t.Errorf("args=%q code=%d, want 2", args, code)
		}
		if stdout != "" || strings.Contains(stderr, secret) || strings.Contains(stderr, "secret-value") {
			t.Errorf("args=%q leaked output: stdout=%q stderr=%q", args, stdout, stderr)
		}
	}
}

func TestHelpAndRootVersion(t *testing.T) {
	code, stdout, stderr := invoke()
	if code != 0 || !strings.HasPrefix(stdout, "mooo-lab ") || stderr != "" {
		t.Fatalf("root: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke("--help")
	if code != 0 || !strings.Contains(stdout, "mooo-lab auth init") || stderr != "" {
		t.Fatalf("help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke("auth", "inspect", "--help")
	if code != 0 || !strings.Contains(stdout, "mooo-lab auth inspect") || stderr != "" {
		t.Fatalf("subcommand help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke("unexpected")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "invalid command") {
		t.Fatalf("unknown command: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestInspectMissingOrCorruptStateIsQuiet(t *testing.T) {
	secret := "/private/synthetic-token/state.json"
	code, stdout, stderr := invoke("auth", "inspect", "--state", secret)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "could not inspect auth state") || strings.Contains(stderr, secret) {
		t.Fatalf("missing state: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
