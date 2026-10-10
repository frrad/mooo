package registration

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/macweb"
)

var ErrInvalidMacClientProfile = errors.New("registration: invalid Mac client profile")

// ApplyMacClientHeaders applies the exact four-header profile observed on the
// current Mac QR generation request. It never adds cookies, authorization, or
// device/account identifiers.
func ApplyMacClientHeaders(request *http.Request, profile macweb.Profile) error {
	if request == nil || !validProfilePart(profile.AppVersion) ||
		!validProfilePart(profile.OSVersion) || !validProfilePart(profile.Language) {
		return ErrInvalidMacClientProfile
	}
	macweb.ApplyHeaders(request, profile, false)
	request.Header.Set("Content-Type", RegistrationJSONContentType)
	return nil
}

// validProfilePart is stricter than macweb.Profile.Validate: the logged-out
// registration request has always rejected whitespace and path separators.
func validProfilePart(value string) bool {
	if value == "" || len(value) > 64 || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsAny(value, " \t\r\n/\\")
}
