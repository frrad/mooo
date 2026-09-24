package registration

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

// The transport boundary is intentionally semantic. It carries the values
// known by the reducer, but does not prescribe URL-form field names, nested
// dictionary encoding, cookies, headers, QR validation, or credentials. Those
// details remain in the reviewed HTTP profile and are not complete enough for
// an implementation here.

var (
	// ErrNoTransport is returned when a coordinator has no transport attached.
	ErrNoTransport = errors.New("registration: no transport")
	// ErrNotTransportEffect is returned when a timer, UI, or handoff effect is
	// accidentally submitted to DispatchTransport.
	ErrNotTransportEffect = errors.New("registration: effect is not transport work")
	// ErrTransportGeneration is returned when a transport result does not belong
	// to the effect that produced its request.
	ErrTransportGeneration = errors.New("registration: transport result generation mismatch")
	// ErrInvalidQRPayload is intentionally static and redacted. It reports only
	// presentation-shape failure; QR URL grammar and check-key validation remain
	// unresolved and are not attempted here.
	ErrInvalidQRPayload = errors.New("registration: invalid QR payload")
)

// MaxQRPayloadBytes bounds the opaque value retained by a presentation owner.
// It is a defensive resource limit, not a claim about the server's URL grammar.
const MaxQRPayloadBytes = 16 * 1024

// Transport executes only registration operations whose semantic inputs and
// results are currently established by the clean-room specification. A future
// HTTP implementation may combine these requests with client-owned identity
// and authentication providers; this interface does not import or discover
// official-client state.
type Transport interface {
	Generate(context.Context, GenerateRequest) (GenerateResponse, error)
	Poll(context.Context, PollRequest) (PollResponse, error)
	Cancel(context.Context, CancelRequest) error
}

// GenerateRequest identifies one challenge-generation operation.
// Credentials and device metadata are intentionally supplied by a future
// transport/input layer because their exact wire encoding is unresolved.
type GenerateRequest struct {
	Generation uint64
	Method     Method
	Mode       Mode
}

// GenerateResponse contains only semantic challenge values accepted by the
// reducer. For QR generation, QRPayload is the complete opaque server value;
// the coordinator parses its transient id before constructing QRGenerated.
type GenerateResponse struct {
	Generation uint64
	Passcode   string
	// QRPayload is the complete opaque server-provided string to render. Its
	// transient id is parsed by the coordinator for subsequent poll/cancel.
	QRPayload    string
	DisplayUntil time.Time
}

// PollRequest identifies one approval poll. ChallengeID is transient and must
// not be treated as a credential.
type PollRequest struct {
	Generation  uint64
	Method      Method
	ChallengeID string
}

// PollResponse is the semantic subset consumed by PollResult. Unknown wire
// statuses must be converted to OutcomeUnknownFailure by the future decoder.
type PollResponse struct {
	Generation      uint64
	Outcome         Outcome
	DeviceAuthCode  string
	DeviceAuthUntil time.Time
}

// CancelRequest identifies an explicit abandonment of an in-progress
// challenge. Cancellation is distinct from established-session logout or
// device revocation.
type CancelRequest struct {
	Generation  uint64
	Method      Method
	ChallengeID string
}

// DispatchResult contains reducer follow-up effects and, only for QR
// generation, the opaque server-provided payload that a presentation owner
// may render. Coordinator does not retain QRPayload; callers should treat it
// as a one-shot value and clear their own copy after rendering or expiry.
type DispatchResult struct {
	Effects   []Effect
	QRPayload string
}

// Coordinator couples the reducer to a mockable semantic transport. It does
// not run timers, render QR data, persist state, or perform network I/O itself.
// Callers apply timer/UI events with Apply and submit only transport effects to
// DispatchTransport.
type Coordinator struct {
	state     State
	transport Transport
}

// NewCoordinator creates a registration coordinator. Method and mode are
// validated by the first Start event, matching New and Reduce semantics.
func NewCoordinator(method Method, mode Mode, transport Transport) *Coordinator {
	return &Coordinator{state: New(method, mode), transport: transport}
}

// State returns the current reducer state by value.
func (c *Coordinator) State() State { return c.state }

// Apply feeds a semantic event to the reducer. The state is committed only
// when reduction succeeds.
func (c *Coordinator) Apply(event Event) ([]Effect, error) {
	next, effects, err := Reduce(c.state, event)
	if err != nil {
		return nil, err
	}
	c.state = next
	return effects, nil
}

// DispatchTransport executes one transport effect and applies its response as
// a reducer event. Non-transport effects are rejected so timer/UI work cannot
// accidentally become network activity. A transport error leaves reducer
// state unchanged; cancellation state is already terminal when its cancel
// request is emitted, so a remote cancellation failure is returned to the
// caller without reviving the local challenge.
func (c *Coordinator) DispatchTransport(ctx context.Context, effect Effect) (DispatchResult, error) {
	switch effect.Kind {
	case EffectGeneratePasscode, EffectGenerateQR:
		if c.transport == nil {
			return DispatchResult{}, ErrNoTransport
		}
		if effect.Generation == 0 || effect.Generation != c.state.Generation || c.state.Phase != PhaseGenerating {
			return DispatchResult{}, ErrStaleEvent
		}
		method := MethodPasscode
		if effect.Kind == EffectGenerateQR {
			method = MethodQR
		}
		response, err := c.transport.Generate(ctx, GenerateRequest{
			Generation: effect.Generation,
			Method:     method,
			Mode:       c.state.Mode,
		})
		if err != nil {
			return DispatchResult{}, err
		}
		if response.Generation != effect.Generation {
			return DispatchResult{}, ErrTransportGeneration
		}
		var qrID string
		if method == MethodQR {
			if !validQRPayload(response.QRPayload) {
				return DispatchResult{}, ErrInvalidQRPayload
			}
			presentation, err := ParseQRPresentation(response.QRPayload)
			if err != nil {
				return DispatchResult{}, err
			}
			qrID = presentation.ID
		}
		event := Event(QRGenerated{
			Generation:   response.Generation,
			QRID:         qrID,
			DisplayUntil: response.DisplayUntil,
		})
		if method == MethodPasscode {
			event = PasscodeGenerated{
				Generation:   response.Generation,
				Code:         response.Passcode,
				DisplayUntil: response.DisplayUntil,
			}
		}
		effects, err := c.Apply(event)
		if err != nil {
			return DispatchResult{}, err
		}
		result := DispatchResult{Effects: effects}
		if method == MethodQR {
			result.QRPayload = response.QRPayload
		}
		return result, nil

	case EffectPollPasscode, EffectPollQR:
		if c.transport == nil {
			return DispatchResult{}, ErrNoTransport
		}
		if effect.Generation == 0 || effect.Generation != c.state.Generation || !awaiting(c.state.Phase) {
			return DispatchResult{}, ErrStaleEvent
		}
		method := MethodPasscode
		if effect.Kind == EffectPollQR {
			method = MethodQR
		}
		response, err := c.transport.Poll(ctx, PollRequest{
			Generation:  effect.Generation,
			Method:      method,
			ChallengeID: effect.ChallengeID,
		})
		if err != nil {
			return DispatchResult{}, err
		}
		if response.Generation != effect.Generation {
			return DispatchResult{}, ErrTransportGeneration
		}
		effects, err := c.Apply(PollResult{
			Generation:      response.Generation,
			Outcome:         response.Outcome,
			DeviceAuthCode:  response.DeviceAuthCode,
			DeviceAuthUntil: response.DeviceAuthUntil,
		})
		if err != nil {
			return DispatchResult{}, err
		}
		return DispatchResult{Effects: effects}, nil

	case EffectCancelPasscode, EffectCancelQR:
		if c.transport == nil {
			return DispatchResult{}, ErrNoTransport
		}
		if effect.Generation == 0 || effect.Generation != c.state.Generation {
			return DispatchResult{}, ErrStaleEvent
		}
		method := MethodPasscode
		if effect.Kind == EffectCancelQR {
			method = MethodQR
		}
		if err := c.transport.Cancel(ctx, CancelRequest{
			Generation:  effect.Generation,
			Method:      method,
			ChallengeID: effect.ChallengeID,
		}); err != nil {
			return DispatchResult{}, err
		}
		return DispatchResult{}, nil

	default:
		return DispatchResult{}, ErrNotTransportEffect
	}
}

// validQRPayload performs only presentation-shape validation. In particular,
// it does not parse a URL, inspect query parameters, or validate a check key.
func validQRPayload(payload string) bool {
	return payload != "" && len(payload) <= MaxQRPayloadBytes && utf8.ValidString(payload)
}
