package registration

import (
	"testing"
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
