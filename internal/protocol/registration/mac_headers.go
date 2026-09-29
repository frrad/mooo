package registration

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

var ErrInvalidMacClientProfile = errors.New("registration: invalid Mac client profile")

// MacClientProfile contains the non-secret compatibility values used by the
// reviewed logged-out Mac registration client.
type MacClientProfile struct {
	AppVersion string
	OSVersion  string
	Language   string
}

// ApplyMacClientHeaders applies the exact four-header profile observed on the
// current Mac QR generation request. It never adds cookies, authorization, or
// device/account identifiers.
func ApplyMacClientHeaders(request *http.Request, profile MacClientProfile) error {
	if request == nil || !validProfilePart(profile.AppVersion) ||
		!validProfilePart(profile.OSVersion) || !validProfilePart(profile.Language) {
		return ErrInvalidMacClientProfile
	}
	request.Header.Set("A", "mac/"+profile.AppVersion+"/"+profile.Language)
	request.Header.Set("Accept-Language", profile.Language)
	request.Header.Set("Content-Type", RegistrationJSONContentType)
	request.Header.Set("User-Agent", "KT/"+profile.AppVersion+" Mc/"+profile.OSVersion+" "+profile.Language)
	return nil
}

func validProfilePart(value string) bool {
	if value == "" || len(value) > 64 || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsAny(value, " \t\r\n/\\")
}
