package registration

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNewHTTPRequestConstructsOnlyReviewedRequest(t *testing.T) {
	form, err := BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: "synthetic password"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := NewHTTPRequest(ctx, form)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" || req.URL.String() != "https://katalk.kakao.com/mac/account/qrCodeLogin/passwordCheck" {
		t.Fatalf("request target = %s %s", req.Method, req.URL)
	}
	if req.Context() != ctx {
		t.Fatal("request did not retain caller context")
	}
	if len(req.Header) != 1 || req.Header.Get("Content-Type") != RegistrationFormContentType {
		t.Fatalf("headers = %#v", req.Header)
	}
	if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || req.Header.Get("X-Signature") != "" {
		t.Fatalf("request added unreviewed headers: %#v", req.Header)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(form.Body) {
		t.Fatalf("body = %q, want %q", body, form.Body)
	}
}

func TestNewHTTPRequestRejectsTamperedProfilesNilContextAndOversizedBody(t *testing.T) {
	form, err := BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ctx  context.Context
		edit func(*FormRequest)
		want error
	}{
		{name: "nil context", want: ErrNilRequestContext},
		{name: "wrong base", ctx: context.Background(), edit: func(f *FormRequest) { f.Profile.BaseURL = "https://other.invalid" }, want: ErrInvalidHTTPProfile},
		{name: "wrong method", ctx: context.Background(), edit: func(f *FormRequest) { f.Profile.Method = HTTPMethod("PUT") }, want: ErrInvalidHTTPProfile},
		{name: "wrong content type", ctx: context.Background(), edit: func(f *FormRequest) { f.ContentType = "application/json" }, want: ErrInvalidHTTPProfile},
		{name: "oversized body", ctx: context.Background(), edit: func(f *FormRequest) { f.Body = []byte(strings.Repeat("x", MaxFormBodyBytes+1)) }, want: ErrFormTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := form
			candidate.Body = append([]byte(nil), form.Body...)
			if test.edit != nil {
				test.edit(&candidate)
			}
			_, err := NewHTTPRequest(test.ctx, candidate)
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("error = %v, want static %v", err, test.want)
			}
		})
	}
}

func TestNewHTTPRequestDoesNotExecuteNetwork(t *testing.T) {
	form, err := BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewHTTPRequest(context.Background(), form)
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("constructor returned nil request")
	}
	// Construction alone cannot produce a response or network side effect.
	if req.Response != nil {
		t.Fatal("constructed request unexpectedly has a response")
	}
}
