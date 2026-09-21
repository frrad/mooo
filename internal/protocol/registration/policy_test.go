package registration

import (
	"testing"
	"time"
)

func TestConfirmedRoutes(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  string
	}{
		{"passcode generate", RoutePasscodeGenerate, "/mac/account/passcodeLogin/generate"},
		{"passcode register", RoutePasscodeRegister, "/mac/account/passcodeLogin/registerDevice"},
		{"passcode cancel", RoutePasscodeCancel, "/mac/account/passcodeLogin/cancel"},
		{"qr generate", RouteQRGenerate, "/mac/account/qrCodeLogin/generate"},
		{"qr cancel", RouteQRCancel, "/mac/account/qrCodeLogin/cancel"},
		{"qr login", RouteQRLogin, "/mac/account/qrCodeLogin/login"},
		{"qr password check", RouteQRPasswordCheck, "/mac/account/qrCodeLogin/passwordCheck"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if string(test.route) != test.want {
				t.Fatalf("route = %q, want %q", test.route, test.want)
			}
		})
	}
}

func TestDecodeQROutcome(t *testing.T) {
	tests := []struct {
		code int
		want Outcome
	}{
		{1, OutcomeUnregisteredDevice},
		{5, OutcomeSuspended},
		{13, OutcomeUnsupportedDevice},
		{14, OutcomePending},
		{15, OutcomeRejected},
		{16, OutcomeExpired},
		{20, OutcomeRestricted},
		{29, OutcomeInvalidResponse},
		{-100, OutcomeUnknownFailure},
		{-1, OutcomeUnknownFailure},
		{0, OutcomeUnknownFailure},
		{2, OutcomeUnknownFailure},
		{12, OutcomeUnknownFailure},
		{17, OutcomeUnknownFailure},
		{28, OutcomeUnknownFailure},
		{30, OutcomeUnknownFailure},
		{100, OutcomeUnknownFailure},
	}
	for i, test := range tests {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			if got := DecodeQROutcome(test.code); got != test.want {
				t.Fatalf("DecodeQROutcome(%d) = %v, want %v", test.code, got, test.want)
			}
		})
	}
}

func TestTimingConstants(t *testing.T) {
	if CountdownTick != time.Second {
		t.Fatalf("CountdownTick = %s, want 1s", CountdownTick)
	}
	if DefaultPollDelay != 3*time.Second || InitialQRPollDelay != 3*time.Second {
		t.Fatalf("poll defaults = %s/%s, want 3s", DefaultPollDelay, InitialQRPollDelay)
	}
}

func TestClampPasscodePollDelay(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"negative", -time.Second, 3 * time.Second},
		{"zero", 0, 3 * time.Second},
		{"just below minimum", 3*time.Second - time.Nanosecond, 3 * time.Second},
		{"minimum", 3 * time.Second, 3 * time.Second},
		{"longer", 8 * time.Second, 8 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClampPasscodePollDelay(test.in); got != test.want {
				t.Fatalf("ClampPasscodePollDelay(%s) = %s, want %s", test.in, got, test.want)
			}
		})
	}
}

func TestQRPollDelay(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"negative", -time.Nanosecond, 3 * time.Second},
		{"zero", 0, 3 * time.Second},
		{"positive subsecond", time.Nanosecond, time.Nanosecond},
		{"positive", 7 * time.Second, 7 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := QRPollDelay(test.in); got != test.want {
				t.Fatalf("QRPollDelay(%s) = %s, want %s", test.in, got, test.want)
			}
		})
	}
}

func TestDeviceAuthPollDelay(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"negative", -time.Nanosecond, 3 * time.Second},
		{"zero", 0, 3 * time.Second},
		{"just below one second", time.Second - time.Nanosecond, 3 * time.Second},
		{"one second", time.Second, time.Second},
		{"longer", 4 * time.Second, 4 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DeviceAuthPollDelay(test.in); got != test.want {
				t.Fatalf("DeviceAuthPollDelay(%s) = %s, want %s", test.in, got, test.want)
			}
		})
	}
}
