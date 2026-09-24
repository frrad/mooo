package registration

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeQRGenerateResponseStrictShapeAndPreservedValues(t *testing.T) {
	body := []byte(`{"status":-997,"url":"synthetic://host?id=a+b","remainingSeconds":12.5}`)
	response, err := DecodeQRGenerateResponse(200, body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != -997 || response.Presentation.Payload != "synthetic://host?id=a+b" || response.RemainingSeconds != 12.5 || response.Presentation.ID != "a+b" {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(fmt.Sprintf("%v", response), "synthetic://host") || strings.Contains(fmt.Sprintf("%#v", response), "synthetic://host") {
		t.Fatal("QR generate formatting leaked URL")
	}
}

func TestDecodeQRGenerateResponseRejectsInvalidStatusURLDelayAndFraming(t *testing.T) {
	tests := []struct {
		name string
		code int
		body []byte
		want error
	}{
		{name: "wrong HTTP", code: 201, body: []byte(`{}`), want: ErrUnexpectedHTTPStatus},
		{name: "missing status", code: 200, body: []byte(`{"url":"synthetic://host?id=x","remainingSeconds":1}`), want: ErrMissingJSONField},
		{name: "fractional status", code: 200, body: []byte(`{"status":1.5,"url":"synthetic://host?id=x","remainingSeconds":1}`), want: ErrWrongJSONType},
		{name: "overflow status", code: 200, body: []byte(`{"status":9223372036854775808,"url":"synthetic://host?id=x","remainingSeconds":1}`), want: ErrWrongJSONType},
		{name: "wrong URL type", code: 200, body: []byte(`{"status":0,"url":3,"remainingSeconds":1}`), want: ErrWrongJSONType},
		{name: "zero delay", code: 200, body: []byte(`{"status":0,"url":"synthetic://host?id=x","remainingSeconds":0}`), want: ErrInvalidQRDelay},
		{name: "negative delay", code: 200, body: []byte(`{"status":0,"url":"synthetic://host?id=x","remainingSeconds":-1}`), want: ErrInvalidQRDelay},
		{name: "infinite delay", code: 200, body: []byte(`{"status":0,"url":"synthetic://host?id=x","remainingSeconds":1e400}`), want: ErrInvalidQRDelay},
		{name: "missing URL id", code: 200, body: []byte(`{"status":0,"url":"synthetic://host?other=x","remainingSeconds":1}`), want: ErrMissingQRID},
		{name: "trailing JSON", code: 200, body: []byte(`{"status":0,"url":"synthetic://host?id=x","remainingSeconds":1}{}`), want: ErrInvalidJSONResponse},
		{name: "top-level array", code: 200, body: []byte(`[]`), want: ErrInvalidJSONResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeQRGenerateResponse(test.code, test.body)
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("error = %v, want static %v", err, test.want)
			}
		})
	}
	_, err := DecodeQRGenerateResponse(200, []byte(strings.Repeat("x", MaxRegistrationJSONBytes+1)))
	if !errors.Is(err, ErrJSONBodyTooLarge) {
		t.Fatalf("oversized body error = %v", err)
	}
}

func TestDecodeQRLoginSuccessPreservesOptionalPresenceAndMinimumPredicate(t *testing.T) {
	body := []byte(`{"status":0,"permanent":true,"user":{"userId":9223372036854775807},"accessToken":"secret-access","refreshToken":"secret-refresh","tokenType":"Bearer","autoLoginAccountId":"account","displayAccountId":"display"}`)
	response, err := DecodeQRLoginSuccess(200, body)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Permanent.Present() || !response.UserPresent || !response.UserID.Present() || !response.AccessToken.Present() || !response.RefreshToken.Present() || !response.TokenType.Present() || !response.AutoLoginAccountID.Present() || !response.DisplayAccountID.Present() {
		t.Fatalf("presence = %#v", response)
	}
	if !response.MinimumFieldsPresent() {
		t.Fatal("minimum completeness predicate rejected justified fields")
	}
	if value, ok := response.AccessToken.Value(); !ok || value != "secret-access" {
		t.Fatalf("access token accessor = %q/%v", value, ok)
	}
	if strings.Contains(fmt.Sprintf("%v", response), "secret-access") || strings.Contains(fmt.Sprintf("%#v", response), "secret-access") || strings.Contains(response.AccessToken.String(), "secret-access") {
		t.Fatal("String/GoString leaked access token")
	}
	minimal, err := DecodeQRLoginSuccess(200, []byte(`{"status":0,"user":{"userId":1},"accessToken":"token"}`))
	if err != nil || !minimal.MinimumFieldsPresent() || minimal.RefreshToken.Present() || minimal.Permanent.Present() {
		t.Fatalf("minimal optional model = %#v, err=%v", minimal, err)
	}
}

func TestDecodeQRLoginSuccessRejectsWrongTypesStatusAndFraming(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
		want error
	}{
		{name: "wrong HTTP", code: 204, body: `{}`, want: ErrUnexpectedHTTPStatus},
		{name: "missing status", code: 200, body: `{"user":{"userId":1}}`, want: ErrMissingJSONField},
		{name: "nonzero status", code: 200, body: `{"status":1}`, want: ErrQRLoginNotSuccessful},
		{name: "fractional status", code: 200, body: `{"status":0.0}`, want: ErrWrongJSONType},
		{name: "wrong permanent", code: 200, body: `{"status":0,"permanent":1}`, want: ErrWrongJSONType},
		{name: "wrong user", code: 200, body: `{"status":0,"user":false}`, want: ErrInvalidJSONResponse},
		{name: "fractional user ID", code: 200, body: `{"status":0,"user":{"userId":1.2}}`, want: ErrWrongJSONType},
		{name: "overflow user ID", code: 200, body: `{"status":0,"user":{"userId":9223372036854775808}}`, want: ErrWrongJSONType},
		{name: "wrong token", code: 200, body: `{"status":0,"accessToken":false}`, want: ErrWrongJSONType},
		{name: "trailing JSON", code: 200, body: `{"status":0}{}`, want: ErrInvalidJSONResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeQRLoginSuccess(test.code, []byte(test.body))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeServerErrorEnvelopeRetainsBoundedRawPresenceAndMapsQRStatus(t *testing.T) {
	body := []byte(`{"status":29,"reason":"secret reason","detailCode":null,"response":{"status":-404,"secret":"value"}}`)
	envelope, err := DecodeServerErrorEnvelope(body)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Status != 29 || envelope.QROutcome() != OutcomeInvalidResponse || !envelope.Reason.Present() || !envelope.DetailCode.Present() || !envelope.Response.Present() {
		t.Fatalf("envelope = %#v", envelope)
	}
	if string(envelope.Reason.Raw()) != `"secret reason"` || string(envelope.DetailCode.Raw()) != "null" {
		t.Fatalf("raw fields not preserved")
	}
	if strings.Contains(fmt.Sprintf("%v", envelope), "secret") || strings.Contains(fmt.Sprintf("%#v", envelope), "secret") || strings.Contains(envelope.Reason.String(), "secret") {
		t.Fatal("error envelope formatting leaked raw values")
	}
	unknown, err := DecodeServerErrorEnvelope([]byte(`{"status":-999}`))
	if err != nil || unknown.QROutcome() != OutcomeUnknownFailure {
		t.Fatalf("unknown outcome = %#v, err=%v", unknown, err)
	}
}

func TestDecodeServerErrorEnvelopeStrictStatusAndBounds(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "missing status", body: `{"reason":"r"}`, want: ErrMissingJSONField},
		{name: "fractional status", body: `{"status":1.2}`, want: ErrWrongJSONType},
		{name: "overflow status", body: `{"status":9223372036854775808}`, want: ErrWrongJSONType},
		{name: "wrong status", body: `{"status":"29"}`, want: ErrWrongJSONType},
		{name: "trailing", body: `{"status":29}{}`, want: ErrInvalidJSONResponse},
		{name: "top-level null", body: `null`, want: ErrInvalidJSONResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeServerErrorEnvelope([]byte(test.body))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	large := `{"status":1,"reason":"` + strings.Repeat("x", MaxRegistrationJSONField) + `"}`
	if _, err := DecodeServerErrorEnvelope([]byte(large)); !errors.Is(err, ErrJSONFieldTooLarge) {
		t.Fatalf("large raw field error = %v", err)
	}
	if _, err := DecodeServerErrorEnvelope([]byte(strings.Repeat("x", MaxRegistrationJSONBytes+1))); !errors.Is(err, ErrJSONBodyTooLarge) {
		t.Fatalf("large body error = %v", err)
	}
}

func TestSensitiveRegistrationFormattingIsRedacted(t *testing.T) {
	marker := "redaction marker"
	values := []any{
		FormRequest{Profile: HTTPRequestProfile{Route: RouteQRLogin}, Body: []byte(marker)},
		QRPresentation{Payload: marker, ID: marker},
		GenerateResponse{Passcode: marker, QRPayload: marker},
		PollResponse{DeviceAuthCode: marker},
	}
	for _, value := range values {
		formatted := fmt.Sprintf("%v %#v %+v", value, value, value)
		if strings.Contains(formatted, marker) {
			t.Fatalf("formatting leaked a sensitive value from %T", value)
		}
	}
}
