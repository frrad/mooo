package registration

import "unicode/utf8"

// MaxQRPayloadBytes bounds the opaque value retained by a presentation owner.
// It is a defensive resource limit, not a claim about the server's URL grammar.
const MaxQRPayloadBytes = 16 * 1024

// validQRPayload performs only presentation-shape validation. In particular,
// it does not parse a URL, inspect query parameters, or validate a check key.
func validQRPayload(payload string) bool {
	return payload != "" && len(payload) <= MaxQRPayloadBytes && utf8.ValidString(payload)
}
