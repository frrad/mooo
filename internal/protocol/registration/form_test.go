package registration

import (
	"context"
	"errors"
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
	if request.Route != RouteQRGenerate {
		t.Fatalf("route = %q", request.Route)
	}
	if request.ContentType != RegistrationJSONContentType {
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

func TestNewHTTPRequestRejectsUnreviewedRouteAndContentType(t *testing.T) {
	built, err := BuildQRLoginRequest(QRLoginRequest{ID: "synthetic-id", Device: UUIDOnlyDevice{UUID: "uuid"}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewHTTPRequest(context.Background(), built)
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != "POST" || request.URL.String() != "https://katalk.kakao.com/mac/account/qrCodeLogin/login" {
		t.Fatalf("request = %s %s", request.Method, request.URL)
	}

	unknownRoute := built
	unknownRoute.Route = Route("/mac/account/passcodeLogin/generate")
	if _, err := NewHTTPRequest(context.Background(), unknownRoute); !errors.Is(err, ErrInvalidHTTPProfile) {
		t.Fatalf("unknown route error = %v", err)
	}
	wrongContentType := built
	wrongContentType.ContentType = "text/plain"
	if _, err := NewHTTPRequest(context.Background(), wrongContentType); !errors.Is(err, ErrInvalidHTTPProfile) {
		t.Fatalf("wrong content type error = %v", err)
	}
}
