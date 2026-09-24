package registration

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildQRGenerateRequestAlamofireGolden(t *testing.T) {
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
	want := "device%5Bmodel%5D=MacBook%20Air%20%5BM%5D&device%5Bname%5D=Mooo%20Lab%20%26%20/?&device%5BosVersion%5D=mac%20OS/26?x&device%5Buuid%5D=u%2Bid%3D1&previousId=prev%20value/?%26%3D%2B"
	if got := string(request.Body); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if strings.Contains(string(request.Body), "+") {
		t.Fatalf("body uses '+' instead of percent escaping: %q", request.Body)
	}
}

func TestBuildQRGenerateRequestOptionalPreviousID(t *testing.T) {
	device := FullDevice{Name: "name", UUID: "uuid", OSVersion: "os", Model: "model"}
	without, err := BuildQRGenerateRequest(QRGenerateRequest{Device: device})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(without.Body); got != "device%5Bmodel%5D=model&device%5Bname%5D=name&device%5BosVersion%5D=os&device%5Buuid%5D=uuid" {
		t.Fatalf("without previousId = %q", got)
	}
	empty := ""
	withEmpty, err := BuildQRGenerateRequest(QRGenerateRequest{PreviousID: &empty, Device: device})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(withEmpty.Body); got != "device%5Bmodel%5D=model&device%5Bname%5D=name&device%5BosVersion%5D=os&device%5Buuid%5D=uuid&previousId=" {
		t.Fatalf("explicit empty previousId = %q", got)
	}
}

func TestBuildPasscodeGenerateRequestBooleanAndEscaping(t *testing.T) {
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
	want := "device%5Bmodel%5D=model&device%5Bname%5D=name&device%5BosVersion%5D=os&device%5Buuid%5D=uuid&email=e%20mail%2B%26&password=p/word?%26&permanent=0"
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
	if !strings.HasSuffix(string(request.Body), "permanent=1") {
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
			body:  "device%5Buuid%5D=uuid&email=email&password=password",
		},
		{
			name: "passcode cancel",
			build: func() (FormRequest, error) {
				return BuildPasscodeCancelRequest(PasscodeCancelRequest{Email: "email", Password: "password", Device: uuidDevice})
			},
			route: RoutePasscodeCancel,
			body:  "device%5Buuid%5D=uuid&email=email&password=password",
		},
		{
			name: "QR cancel",
			build: func() (FormRequest, error) {
				return BuildQRCancelRequest(QRCancelRequest{ID: "qr-id", Device: uuidDevice})
			},
			route: RouteQRCancel,
			body:  "device%5Buuid%5D=uuid&id=qr-id",
		},
		{
			name: "QR login",
			build: func() (FormRequest, error) {
				return BuildQRLoginRequest(QRLoginRequest{ID: "qr-id", Device: uuidDevice})
			},
			route: RouteQRLogin,
			body:  "device%5Buuid%5D=uuid&id=qr-id",
		},
		{
			name: "QR password check",
			build: func() (FormRequest, error) {
				return BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: "password"})
			},
			route: RouteQRPasswordCheck,
			body:  "password=password",
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
