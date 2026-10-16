package connector

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// hostComputerName reads the macOS Computer Name, the user-facing name from
// System Settings. It reports false when the name cannot be read.
func hostComputerName() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(string(out), "\n"), true
}

// hostHardwareModel reads hw.model, for example "Mac14,2". It reports false
// when the value is unavailable or empty.
func hostHardwareModel() (string, bool) {
	model, err := unix.Sysctl("hw.model")
	if err != nil || model == "" {
		return "", false
	}
	return model, true
}
