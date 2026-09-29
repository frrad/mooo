package registration

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildQRGenerateRequestJSONGolden(t *testing.T) {
	previousID := "prev value/?&=+"
	request, err := BuildQRGenerateRequest(QRGenerateRequest{
		PreviousID: &previousID,
		Device: FullDevice{
			Name:      "Mooo Lab & /?",
			UUID:      "u+id=1",
			OSVersion: "mac OS/26?x",
			Model:     "MacBook Air [M]",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Profile.Route != RouteQRGenerate || request.Profile.Method != HTTPMethodPost {
		t.Fatalf("profile = %#v", request.Profile)
	}
	if request.ContentType != RegistrationFormContentType {
		t.Fatalf("content type = %q", request.ContentType)
	}
	want := `{"device":{"model":"MacBook Air [M]","name":"Mooo Lab & /?","osVersion":"mac OS/26?x","uuid":"u+id=1"},"previousId":"prev value/?&=+"}`
	if got := string(request.Body); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestBuildQRGenerateRequestOptionalPreviousID(t *testing.T) {
	device := FullDevice{Name: "name", UUID: "uuid", OSVersion: "os", Model: "model"}
	without, err := BuildQRGenerateRequest(QRGenerateRequest{Device: device})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(without.Body); got != `{"device":{"model":"model","name":"name","osVersion":"os","uuid":"uuid"}}` {
		t.Fatalf("without previousId = %q", got)
	}
	empty := ""
	withEmpty, err := BuildQRGenerateRequest(QRGenerateRequest{PreviousID: &empty, Device: device})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(withEmpty.Body); got != `{"device":{"model":"model","name":"name","osVersion":"os","uuid":"uuid"},"previousId":""}` {
		t.Fatalf("explicit empty previousId = %q", got)
	}
}

func TestBuildPasscodeGenerateRequestBooleanAndJSONEscaping(t *testing.T) {
	request, err := BuildPasscodeGenerateRequest(PasscodeGenerateRequest{
		Email:     "e mail+&",
		Password:  "p/word?&",
		Permanent: false,
		Device: FullDevice{
			Name:      "name",
			UUID:      "uuid",
			OSVersion: "os",
			Model:     "model",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"device":{"model":"model","name":"name","osVersion":"os","uuid":"uuid"},"email":"e mail+&","password":"p/word?&","permanent":false}`
	if got := string(request.Body); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	request, err = BuildPasscodeGenerateRequest(PasscodeGenerateRequest{
		Email: "email", Password: "password", Permanent: true,
		Device: FullDevice{Name: "name", UUID: "uuid", OSVersion: "os", Model: "model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(request.Body), `"permanent":true}`) {
		t.Fatalf("true permanent encoding = %q", request.Body)
	}
}

func TestBuildRemainingRegistrationForms(t *testing.T) {
	uuidDevice := UUIDOnlyDevice{UUID: "uuid"}
	tests := []struct {
		name  string
		build func() (FormRequest, error)
		route Route
		body  string
	}{
		{
			name: "passcode register",
			build: func() (FormRequest, error) {
				return BuildPasscodeRegisterRequest(PasscodeRegisterRequest{Email: "email", Password: "password", Device: uuidDevice})
			},
			route: RoutePasscodeRegister,
			body:  `{"device":{"uuid":"uuid"},"email":"email","password":"password"}`,
		},
		{
			name: "passcode cancel",
			build: func() (FormRequest, error) {
				return BuildPasscodeCancelRequest(PasscodeCancelRequest{Email: "email", Password: "password", Device: uuidDevice})
			},
			route: RoutePasscodeCancel,
			body:  `{"device":{"uuid":"uuid"},"email":"email","password":"password"}`,
		},
		{
			name: "QR cancel",
			build: func() (FormRequest, error) {
				return BuildQRCancelRequest(QRCancelRequest{ID: "qr-id", Device: uuidDevice})
			},
			route: RouteQRCancel,
			body:  `{"device":{"uuid":"uuid"},"id":"qr-id"}`,
		},
		{
			name: "QR login",
			build: func() (FormRequest, error) {
				return BuildQRLoginRequest(QRLoginRequest{ID: "qr-id", Device: uuidDevice})
			},
			route: RouteQRLogin,
			body:  `{"device":{"uuid":"uuid"},"id":"qr-id"}`,
		},
		{
			name: "QR password check",
			build: func() (FormRequest, error) {
				return BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: "password"})
			},
			route: RouteQRPasswordCheck,
			body:  `{"password":"password"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := test.build()
			if err != nil {
				t.Fatal(err)
			}
			if request.Profile.Route != test.route || request.Profile.Method != HTTPMethodPost {
				t.Fatalf("profile = %#v", request.Profile)
			}
			if got := string(request.Body); got != test.body {
				t.Fatalf("body = %q, want %q", got, test.body)
			}
			if request.ContentType != RegistrationFormContentType {
				t.Fatalf("content type = %q", request.ContentType)
			}
		})
	}
}

func TestFormBuildersRejectIncompleteValuesWithoutEchoingThem(t *testing.T) {
	missing := "sensitive-password"
	_, err := BuildQRGenerateRequest(QRGenerateRequest{
		Device: FullDevice{Name: missing, UUID: "", OSVersion: "os", Model: "model"},
	})
	if !errors.Is(err, ErrMissingFormField) || err.Error() != ErrMissingFormField.Error() {
		t.Fatalf("missing QR field error = %v", err)
	}
	_, err = BuildPasscodeGenerateRequest(PasscodeGenerateRequest{
		Email: "email", Password: missing,
		Device: FullDevice{Name: "name", UUID: "uuid", OSVersion: "", Model: "model"},
	})
	if !errors.Is(err, ErrMissingFormField) || strings.Contains(err.Error(), missing) {
		t.Fatalf("missing passcode field error = %v", err)
	}
	invalid := string([]byte{0xff})
	_, err = BuildQRGenerateRequest(QRGenerateRequest{
		PreviousID: &invalid,
		Device:     FullDevice{Name: "name", UUID: "uuid", OSVersion: "os", Model: "model"},
	})
	if !errors.Is(err, ErrInvalidFormRequest) || err.Error() != ErrInvalidFormRequest.Error() {
		t.Fatalf("invalid previousId error = %v", err)
	}
	tooLarge := strings.Repeat("x", MaxFormFieldBytes+1)
	_, err = BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: tooLarge})
	if !errors.Is(err, ErrFormTooLarge) || err.Error() != ErrFormTooLarge.Error() {
		t.Fatalf("oversized password error = %v", err)
	}
}
