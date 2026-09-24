package registration

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

var (
	// ErrInvalidQRPresentation is static and redacted. It covers malformed URL
	// syntax, invalid UTF-8/shape, and invalid query decoding.
	ErrInvalidQRPresentation = errors.New("registration: invalid QR presentation")
	// ErrMissingQRID is static and redacted; the payload is never included.
	ErrMissingQRID = errors.New("registration: QR presentation missing id")
	// ErrAmbiguousQRID is static and redacted; duplicate values are not exposed.
	ErrAmbiguousQRID = errors.New("registration: QR presentation has ambiguous id")
)

// QRPresentation preserves the complete server-provided payload while
// exposing the single query id used by subsequent poll/cancel operations.
// Scheme, host, other query fields, and check-key validation are intentionally
// outside this parser because current Mac evidence does not establish them.
type QRPresentation struct {
	Payload string
	ID      string
}

// ParseQRPresentation parses normal URL components and extracts exactly one
// non-empty id query value. It performs no scheme/host allowlisting and does
// not validate an unresolved QR check key.
func ParseQRPresentation(payload string) (QRPresentation, error) {
	if !validQRPayload(payload) {
		return QRPresentation{}, ErrInvalidQRPresentation
	}
	parsed, err := url.Parse(payload)
	if err != nil {
		return QRPresentation{}, ErrInvalidQRPresentation
	}
	ids, err := queryIDs(parsed.RawQuery)
	if err != nil {
		return QRPresentation{}, err
	}
	if len(ids) > 1 {
		return QRPresentation{}, ErrAmbiguousQRID
	}
	if len(ids) == 0 || ids[0] == "" {
		return QRPresentation{}, ErrMissingQRID
	}
	if !utf8.ValidString(ids[0]) {
		return QRPresentation{}, ErrInvalidQRPresentation
	}
	return QRPresentation{Payload: payload, ID: ids[0]}, nil
}

// queryIDs performs the small query-item operation needed here instead of
// url.ParseQuery: Foundation URLComponents preserves a raw '+' in a query
// value, while ParseQuery applies application/x-www-form-urlencoded semantics
// and turns it into a space. Percent escapes are still decoded normally.
func queryIDs(rawQuery string) ([]string, error) {
	if rawQuery == "" {
		return nil, nil
	}
	var ids []string
	for _, item := range strings.Split(rawQuery, "&") {
		if item == "" {
			continue
		}
		namePart, valuePart := item, ""
		if equal := strings.IndexByte(item, '='); equal >= 0 {
			namePart, valuePart = item[:equal], item[equal+1:]
		}
		name, err := url.PathUnescape(namePart)
		if err != nil {
			return nil, ErrInvalidQRPresentation
		}
		if name != "id" {
			continue
		}
		value, err := url.PathUnescape(valuePart)
		if err != nil {
			return nil, ErrInvalidQRPresentation
		}
		if !utf8.ValidString(value) {
			return nil, ErrInvalidQRPresentation
		}
		ids = append(ids, value)
	}
	return ids, nil
}
