package connector

import (
	"errors"
	"testing"
)

func TestDeviceNameFromHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		err  error
		want string
	}{
		{"Studio-Mac.local", nil, "Studio-Mac"},
		{"home", nil, "home"},
		{"", nil, "mooo-bridge"},
		{" .local", nil, "mooo-bridge"},
		{"ignored", errors.New("no hostname"), "mooo-bridge"},
	} {
		if got := deviceNameFromHost(tc.host, tc.err); got != tc.want {
			t.Errorf("deviceNameFromHost(%q, %v) = %q, want %q", tc.host, tc.err, got, tc.want)
		}
	}
}
