package sessionlogin

import (
	"errors"
	"fmt"

	"github.com/frrad/mooo/internal/protocol/loco"
)

var (
	ErrReceiptNotEligible    = errors.New("sessionlogin: receipt notice is not eligible")
	ErrReceiptMethodMismatch = errors.New("sessionlogin: receipt method does not match header")
	ErrReceiptNilBody        = errors.New("sessionlogin: receipt body is nil")
	ErrReceiptFieldType      = errors.New("sessionlogin: unsupported receipt field type")
)

// IncomingReceiptInput is the scalar-projection boundary after LOCO framing,
// BSON decoding, and the source notice constructor/validation. A nil Body is
// distinct from an allocated empty dictionary. This type does not decode BSON,
// validate nested notice fields, allocate an ID, or retain transport state.
type IncomingReceiptInput struct {
	Header loco.Header
	Method string
	Body   map[string]any
}

// ProjectIncomingReceiptBody converts one source-qualified HINT or BLOCKSYNC
// notice that has already passed its notice-level constructor/validation into
// the existing typed receipt input. It is deliberately only a scalar
// projection: unrelated nested fields and complete eligibility are outside
// this boundary. HINT accepts an allocated empty dictionary. BLOCKSYNC
// revision fields default to signed int32 zero when absent or represented by
// SGJSONNull. Unsupported NSNumber coercions remain a separate dependency
// (207); this bounded unit accepts int32 only.
func ProjectIncomingReceiptBody(input IncomingReceiptInput) (ReceiptBody, error) {
	if input.Method != "HINT" && input.Method != "BLOCKSYNC" {
		return ReceiptBody{}, fmt.Errorf("%w: %q", ErrReceiptNotEligible, input.Method)
	}
	if input.Header.Method != input.Method {
		return ReceiptBody{}, fmt.Errorf("%w: header=%q method=%q", ErrReceiptMethodMismatch, input.Header.Method, input.Method)
	}
	if input.Body == nil {
		return ReceiptBody{}, ErrReceiptNilBody
	}

	body := ReceiptBody{PacketID: input.Header.PacketID}
	if input.Method == "HINT" {
		body.Kind = ReceiptBodyHint
		return body, nil
	}
	body.Kind = ReceiptBodyBlockSync
	// The incoming source mapping is the reverse of the outgoing builder:
	// source r/pr become typed revision/plusRevision fields.
	revision, err := projectReceiptInt32Field(input.Body, "r", "revision")
	if err != nil {
		return ReceiptBody{}, err
	}
	plusRevision, err := projectReceiptInt32Field(input.Body, "pr", "plusRevision")
	if err != nil {
		return ReceiptBody{}, err
	}
	body.Revision = revision
	body.PlusRevision = plusRevision
	return body, nil
}

func projectReceiptInt32Field(fields map[string]any, source, destination string) (int32, error) {
	if value, ok := fields[source]; ok {
		if !isReceiptJSONNull(value) {
			// Source mapping replaces the destination before typed KVC. An
			// invalid stale destination must not reject a valid source.
			return receiptInt32(value, source)
		}
	}
	if value, ok := fields[destination]; ok && !isReceiptJSONNull(value) {
		return receiptInt32(value, destination)
	}
	return 0, nil
}

func receiptInt32(value any, field string) (int32, error) {
	result, ok := value.(int32)
	if !ok {
		return 0, fmt.Errorf("%w: %s has %T; NSNumber coercion is outside this contract", ErrReceiptFieldType, field, value)
	}
	return result, nil
}
