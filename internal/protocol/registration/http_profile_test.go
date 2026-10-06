package registration

import "testing"

func TestProfileForConfirmedRoutes(t *testing.T) {
	tests := []struct {
		name   string
		route  Route
		fields FieldMask
		device DeviceShape
	}{
		{"passcode generate", RoutePasscodeGenerate, FieldEmail | FieldPassword | FieldPermanent, DeviceFull},
		{"passcode register", RoutePasscodeRegister, FieldEmail | FieldPassword, DeviceUUIDOnly},
		{"passcode cancel", RoutePasscodeCancel, FieldEmail | FieldPassword, DeviceUUIDOnly},
		{"qr generate", RouteQRGenerate, FieldPreviousID, DeviceFull},
		{"qr cancel", RouteQRCancel, FieldID, DeviceUUIDOnly},
		{"qr login", RouteQRLogin, FieldID, DeviceUUIDOnly},
		{"qr password check", RouteQRPasswordCheck, FieldPassword, DeviceNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, ok := ProfileFor(test.route)
			if !ok {
				t.Fatal("ProfileFor rejected confirmed route")
			}
			if profile.BaseURL != RegistrationBaseURL || profile.Method != HTTPMethodPost || profile.Route != test.route {
				t.Fatalf("profile metadata = %#v", profile)
			}
			if profile.Form.Fields != test.fields || profile.Form.Device != test.device {
				t.Fatalf("profile form = %#v", profile.Form)
			}
		})
	}
	if _, ok := ProfileFor(Route("/synthetic/unknown")); ok {
		t.Fatal("unknown route received guessed profile")
	}
}
