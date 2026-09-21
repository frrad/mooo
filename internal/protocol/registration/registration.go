// Package registration models the semantic state machine used by secondary
// device registration. It deliberately contains no transport, clock, storage,
// or credential handling. Callers supply events and execute the returned
// effects in their own layers.
package registration

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Method is the presentation used to register a secondary device.
type Method uint8

const (
	MethodPasscode Method = iota + 1
	MethodQR
)

func (m Method) valid() bool { return m == MethodPasscode || m == MethodQR }

// Mode controls whether a successful registration is permanent or temporary.
type Mode uint8

const (
	ModePermanent Mode = iota + 1
	ModeTemporary
)

func (m Mode) valid() bool { return m == ModePermanent || m == ModeTemporary }

// Phase is the externally meaningful lifecycle phase of a registration.
type Phase uint8

const (
	PhaseIdle Phase = iota
	PhaseGenerating
	PhaseAwaitingApproval
	PhaseAwaitingDeviceAuthorization
	PhaseAwaitingRestore
	PhaseHandedOff
	PhaseExpired
	PhaseCancelled
	PhaseFailed
)

// Outcome is the semantic result of an approval poll. Unknown wire values
// must be decoded as OutcomeUnknownFailure, or rejected before reaching this
// package; they must never be treated as approval.
type Outcome uint8

const (
	OutcomeNone Outcome = iota
	OutcomePending
	OutcomeApproved
	OutcomeUnregisteredDevice
	OutcomeRejected
	OutcomeExpired
	OutcomeUnsupportedDevice
	OutcomeSuspended
	OutcomeRestricted
	OutcomeInvalidResponse
	OutcomeUnknownFailure
)

func (o Outcome) valid() bool {
	return o >= OutcomePending && o <= OutcomeUnknownFailure
}

// RestoreChoice is the permanent-login history decision.
type RestoreChoice uint8

const (
	RestoreNotRequested RestoreChoice = iota
	RestoreHistory
	SkipHistory
)

func (c RestoreChoice) valid() bool { return c == RestoreHistory || c == SkipHistory }

// Error values returned when an event cannot be applied. An invalid event
// leaves the state and effects unchanged.
var (
	ErrInvalidTransition = errors.New("registration: invalid transition")
	ErrStaleEvent        = errors.New("registration: stale event")
	ErrInvalidChallenge  = errors.New("registration: invalid generated challenge")
	ErrInvalidDeadline   = errors.New("registration: invalid deadline")
	ErrInvalidOutcome    = errors.New("registration: invalid outcome")
)

// Challenge contains only short-lived presentation values. It intentionally
// contains no account password, session credential, token, or device secret.
type Challenge struct {
	Passcode string
	QRID     string
}

// Timers records the independent lifecycle timers. A poll timer remains
// active while a QR device-authorization timer is active.
type Timers struct {
	Display    bool
	Poll       bool
	DeviceAuth bool
}

// State is the complete deterministic reducer state. Generation is a
// session token: every asynchronous event must carry the generation that
// created it.
type State struct {
	Method     Method
	Mode       Mode
	Phase      Phase
	Generation uint64

	Challenge       Challenge
	DisplayUntil    time.Time
	DeviceAuthCode  string
	DeviceAuthUntil time.Time
	Timers          Timers

	Outcome Outcome
	Restore RestoreChoice
}

// New creates an idle reducer. Method and mode are validated when Start is
// applied, so constructing a zero-value or untrusted State cannot bypass the
// fail-closed transition checks.
func New(method Method, mode Mode) State {
	return State{Method: method, Mode: mode, Phase: PhaseIdle}
}

// Reduce applies one semantic event without consulting wall-clock time or
// external state. On error, the returned state is byte-for-byte equivalent in
// meaning to the input and no effects are returned.
func Reduce(s State, event Event) (State, []Effect, error) {
	if event == nil {
		return s, nil, ErrInvalidTransition
	}

	switch e := event.(type) {
	case Start:
		return start(s)
	case PasscodeGenerated:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Method != MethodPasscode || s.Phase != PhaseGenerating {
			return s, nil, ErrInvalidTransition
		}
		if !validPasscode(e.Code) {
			return s, nil, ErrInvalidChallenge
		}
		if !validDeadline(e.DisplayUntil) {
			return s, nil, ErrInvalidDeadline
		}
		s.Phase = PhaseAwaitingApproval
		s.Challenge = Challenge{Passcode: e.Code}
		s.DisplayUntil = e.DisplayUntil
		s.Outcome = OutcomeNone
		s.Timers = Timers{Display: true, Poll: true}
		return s, []Effect{
			{Kind: EffectScheduleDisplayExpiry, Generation: s.Generation, Deadline: e.DisplayUntil},
			{Kind: EffectSchedulePoll, Generation: s.Generation},
		}, nil
	case QRGenerated:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Method != MethodQR || s.Phase != PhaseGenerating {
			return s, nil, ErrInvalidTransition
		}
		if strings.TrimSpace(e.QRID) == "" || !utf8.ValidString(e.QRID) {
			return s, nil, ErrInvalidChallenge
		}
		if !validDeadline(e.DisplayUntil) {
			return s, nil, ErrInvalidDeadline
		}
		s.Phase = PhaseAwaitingApproval
		s.Challenge = Challenge{QRID: e.QRID}
		s.DisplayUntil = e.DisplayUntil
		s.Outcome = OutcomeNone
		s.Timers = Timers{Display: true, Poll: true}
		return s, []Effect{
			{Kind: EffectScheduleDisplayExpiry, Generation: s.Generation, Deadline: e.DisplayUntil},
			{Kind: EffectSchedulePoll, Generation: s.Generation},
		}, nil
	case PollDue:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if !s.Timers.Poll || !awaiting(s.Phase) {
			return s, nil, ErrInvalidTransition
		}
		if s.Method == MethodPasscode && s.Challenge.Passcode == "" {
			return s, nil, ErrInvalidChallenge
		}
		if s.Method == MethodQR && strings.TrimSpace(s.Challenge.QRID) == "" {
			return s, nil, ErrInvalidChallenge
		}
		kind := EffectPollPasscode
		if s.Method == MethodQR {
			kind = EffectPollQR
		}
		return s, []Effect{{Kind: kind, Generation: s.Generation, ChallengeID: challengeID(s)}}, nil
	case PollResult:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		return applyPollResult(s, e)
	case DeviceAuthorizationRequired:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Method != MethodQR || s.Phase != PhaseAwaitingApproval {
			return s, nil, ErrInvalidTransition
		}
		if strings.TrimSpace(s.Challenge.QRID) == "" {
			return s, nil, ErrInvalidChallenge
		}
		if !validPasscode(e.Code) {
			return s, nil, ErrInvalidChallenge
		}
		if !validDeadline(e.ExpiresAt) {
			return s, nil, ErrInvalidDeadline
		}
		s.Phase = PhaseAwaitingDeviceAuthorization
		s.Outcome = OutcomeUnregisteredDevice
		s.DeviceAuthCode = e.Code
		s.DeviceAuthUntil = e.ExpiresAt
		s.Timers.DeviceAuth = true
		return s, []Effect{
			{Kind: EffectScheduleDeviceAuthExpiry, Generation: s.Generation, Deadline: e.ExpiresAt},
			{Kind: EffectSchedulePoll, Generation: s.Generation},
		}, nil
	case DisplayExpired:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if !awaiting(s.Phase) {
			return s, nil, ErrInvalidTransition
		}
		s.Phase = PhaseExpired
		s.Outcome = OutcomeExpired
		effects := stopTimers(&s)
		effects = append(effects, cancelEffect(s))
		clearChallenge(&s)
		return s, effects, nil
	case DeviceAuthExpired:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Method != MethodQR || s.Phase != PhaseAwaitingDeviceAuthorization {
			return s, nil, ErrInvalidTransition
		}
		s.Phase = PhaseExpired
		s.Outcome = OutcomeExpired
		effects := stopTimers(&s)
		effects = append(effects, cancelEffect(s))
		clearChallenge(&s)
		return s, effects, nil
	case Cancel:
		// Cancellation is deliberately idempotent after any terminal result,
		// including expiry. This also makes double-close safe.
		if terminal(s.Phase) {
			return s, nil, nil
		}
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Phase != PhaseGenerating && !awaiting(s.Phase) {
			return s, nil, ErrInvalidTransition
		}
		hadChallenge := hasChallenge(s)
		s.Phase = PhaseCancelled
		s.Outcome = OutcomeNone
		effects := stopTimers(&s)
		if hadChallenge {
			effects = append(effects, cancelEffect(s))
		}
		clearChallenge(&s)
		return s, effects, nil
	case Refresh:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Method != MethodQR || s.Phase != PhaseExpired {
			return s, nil, ErrInvalidTransition
		}
		if !advanceGeneration(&s) {
			return s, nil, ErrInvalidTransition
		}
		s.Phase = PhaseGenerating
		s.Outcome = OutcomeNone
		s.Restore = RestoreNotRequested
		clearChallenge(&s)
		s.Timers = Timers{}
		return s, []Effect{{Kind: EffectGenerateQR, Generation: s.Generation}}, nil
	case RestoreSelected:
		if err := checkGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Method != MethodQR && s.Method != MethodPasscode {
			return s, nil, ErrInvalidTransition
		}
		if s.Mode != ModePermanent || s.Phase != PhaseAwaitingRestore || !e.Choice.valid() {
			return s, nil, ErrInvalidTransition
		}
		s.Restore = e.Choice
		s.Phase = PhaseHandedOff
		s.Timers = Timers{}
		return s, []Effect{{Kind: EffectHandoffPermanent, Generation: s.Generation, Restore: e.Choice}}, nil
	default:
		return s, nil, ErrInvalidTransition
	}
}

// Apply is the method form of Reduce for callers that keep a reducer state.
func (s State) Apply(event Event) (State, []Effect, error) { return Reduce(s, event) }

func start(s State) (State, []Effect, error) {
	if !s.Method.valid() || !s.Mode.valid() || s.Phase != PhaseIdle {
		return s, nil, ErrInvalidTransition
	}
	if !advanceGeneration(&s) {
		return s, nil, ErrInvalidTransition
	}
	s.Phase = PhaseGenerating
	s.Outcome = OutcomeNone
	s.Restore = RestoreNotRequested
	kind := EffectGeneratePasscode
	if s.Method == MethodQR {
		kind = EffectGenerateQR
	}
	return s, []Effect{{Kind: kind, Generation: s.Generation}}, nil
}

func advanceGeneration(s *State) bool {
	if s.Generation == ^uint64(0) {
		return false
	}
	s.Generation++
	return s.Generation != 0
}

func applyPollResult(s State, e PollResult) (State, []Effect, error) {
	if !awaiting(s.Phase) || !e.Outcome.valid() {
		return s, nil, ErrInvalidOutcome
	}
	if (s.Method == MethodPasscode && !validPasscode(s.Challenge.Passcode)) ||
		(s.Method == MethodQR && (strings.TrimSpace(s.Challenge.QRID) == "" || !utf8.ValidString(s.Challenge.QRID))) {
		return s, nil, ErrInvalidChallenge
	}
	if e.Outcome == OutcomeUnregisteredDevice {
		if s.Method != MethodQR || s.Phase != PhaseAwaitingApproval {
			return s, nil, ErrInvalidOutcome
		}
		if strings.TrimSpace(s.Challenge.QRID) == "" {
			return s, nil, ErrInvalidChallenge
		}
		if !validPasscode(e.DeviceAuthCode) {
			return s, nil, ErrInvalidChallenge
		}
		if !validDeadline(e.DeviceAuthUntil) {
			return s, nil, ErrInvalidDeadline
		}
		s.Phase = PhaseAwaitingDeviceAuthorization
		s.Outcome = OutcomeUnregisteredDevice
		s.DeviceAuthCode = e.DeviceAuthCode
		s.DeviceAuthUntil = e.DeviceAuthUntil
		s.Timers.DeviceAuth = true
		return s, []Effect{
			{Kind: EffectScheduleDeviceAuthExpiry, Generation: s.Generation, Deadline: e.DeviceAuthUntil},
			{Kind: EffectSchedulePoll, Generation: s.Generation},
		}, nil
	}
	if s.Method == MethodPasscode && !passcodeOutcome(e.Outcome) {
		return s, nil, ErrInvalidOutcome
	}
	if s.Method == MethodQR && !qrOutcome(e.Outcome) {
		return s, nil, ErrInvalidOutcome
	}
	if e.Outcome == OutcomePending {
		s.Outcome = OutcomePending
		return s, []Effect{{Kind: EffectSchedulePoll, Generation: s.Generation}}, nil
	}
	if e.Outcome == OutcomeApproved {
		return approve(s)
	}
	s.Outcome = e.Outcome
	s.Phase = PhaseFailed
	if e.Outcome == OutcomeExpired {
		s.Phase = PhaseExpired
	}
	effects := stopTimers(&s)
	clearChallenge(&s)
	return s, effects, nil
}

func approve(s State) (State, []Effect, error) {
	s.Outcome = OutcomeApproved
	effects := stopTimers(&s)
	clearChallenge(&s)
	if s.Mode == ModePermanent {
		s.Phase = PhaseAwaitingRestore
		s.Restore = RestoreNotRequested
		effects = append(effects, Effect{Kind: EffectOfferRestoreOrSkip, Generation: s.Generation})
		return s, effects, nil
	}
	s.Phase = PhaseHandedOff
	s.Restore = RestoreNotRequested
	effects = append(effects, Effect{Kind: EffectHandoffTemporary, Generation: s.Generation})
	return s, effects, nil
}

func checkGeneration(s State, generation uint64) error {
	if generation == 0 || generation != s.Generation {
		return ErrStaleEvent
	}
	return nil
}

func validPasscode(code string) bool {
	return utf8.ValidString(code) && utf8.RuneCountInString(code) == 4
}

func validDeadline(deadline time.Time) bool { return !deadline.IsZero() }

func awaiting(p Phase) bool {
	return p == PhaseAwaitingApproval || p == PhaseAwaitingDeviceAuthorization
}

func terminal(p Phase) bool {
	return p == PhaseAwaitingRestore || p == PhaseHandedOff || p == PhaseExpired || p == PhaseCancelled || p == PhaseFailed
}

func passcodeOutcome(o Outcome) bool {
	return o == OutcomePending || o == OutcomeApproved || o == OutcomeRejected ||
		o == OutcomeExpired || o == OutcomeInvalidResponse || o == OutcomeUnknownFailure
}

func qrOutcome(o Outcome) bool {
	return o == OutcomePending || o == OutcomeApproved || o == OutcomeRejected ||
		o == OutcomeExpired || o == OutcomeUnsupportedDevice || o == OutcomeSuspended ||
		o == OutcomeRestricted || o == OutcomeInvalidResponse || o == OutcomeUnknownFailure
}

func challengeID(s State) string {
	if s.Method == MethodPasscode {
		return s.Challenge.Passcode
	}
	return s.Challenge.QRID
}

func hasChallenge(s State) bool { return challengeID(s) != "" }

func clearChallenge(s *State) {
	s.Challenge = Challenge{}
	s.DisplayUntil = time.Time{}
	s.DeviceAuthCode = ""
	s.DeviceAuthUntil = time.Time{}
}

func stopTimers(s *State) []Effect {
	var effects []Effect
	if s.Timers.Display {
		effects = append(effects, Effect{Kind: EffectStopDisplayTimer, Generation: s.Generation})
	}
	if s.Timers.Poll {
		effects = append(effects, Effect{Kind: EffectStopPoll, Generation: s.Generation})
	}
	if s.Timers.DeviceAuth {
		effects = append(effects, Effect{Kind: EffectStopDeviceAuthTimer, Generation: s.Generation})
	}
	s.Timers = Timers{}
	return effects
}

func cancelEffect(s State) Effect {
	kind := EffectCancelPasscode
	if s.Method == MethodQR {
		kind = EffectCancelQR
	}
	return Effect{Kind: kind, Generation: s.Generation, ChallengeID: challengeID(s)}
}

// Event is a semantic input to Reduce. All asynchronous events except Start
// carry a Generation field and are rejected when they belong to another run.
type Event interface{ registrationEvent() }

// Start begins generation of a new challenge.
type Start struct{}

func (Start) registrationEvent() {}

// PasscodeGenerated is the result of a passcode generation request.
type PasscodeGenerated struct {
	Generation   uint64
	Code         string
	DisplayUntil time.Time
}

func (PasscodeGenerated) registrationEvent() {}

// QRGenerated is the result of a QR generation request.
type QRGenerated struct {
	Generation   uint64
	QRID         string
	DisplayUntil time.Time
}

func (QRGenerated) registrationEvent() {}

// PollDue is emitted by the caller's approval-poll timer.
type PollDue struct{ Generation uint64 }

func (PollDue) registrationEvent() {}

// PollResult is the semantic result of a passcode or QR approval poll.
type PollResult struct {
	Generation      uint64
	Outcome         Outcome
	DeviceAuthCode  string
	DeviceAuthUntil time.Time
}

func (PollResult) registrationEvent() {}

// DeviceAuthorizationRequired moves a QR flow into its short-lived device
// authorization phase while retaining the independent approval poll.
type DeviceAuthorizationRequired struct {
	Generation uint64
	Code       string
	ExpiresAt  time.Time
}

func (DeviceAuthorizationRequired) registrationEvent() {}

// DisplayExpired ends the displayed challenge and all associated work.
type DisplayExpired struct{ Generation uint64 }

func (DisplayExpired) registrationEvent() {}

// DeviceAuthExpired ends a QR device-authorization step and clears its QR
// identifier and short code.
type DeviceAuthExpired struct{ Generation uint64 }

func (DeviceAuthExpired) registrationEvent() {}

// Cancel abandons a run. Applying it again after cancellation or expiry is a
// successful no-op.
type Cancel struct{ Generation uint64 }

func (Cancel) registrationEvent() {}

// Refresh starts a new QR generation after an expired QR presentation.
type Refresh struct{ Generation uint64 }

func (Refresh) registrationEvent() {}

// RestoreSelected completes the permanent restore-or-skip decision.
type RestoreSelected struct {
	Generation uint64
	Choice     RestoreChoice
}

func (RestoreSelected) registrationEvent() {}

// EffectKind identifies work that a transport, timer, or UI coordinator may
// perform. Effects are requests only; this package never performs them.
type EffectKind uint8

const (
	EffectGeneratePasscode EffectKind = iota + 1
	EffectGenerateQR
	EffectScheduleDisplayExpiry
	EffectSchedulePoll
	EffectPollPasscode
	EffectPollQR
	EffectScheduleDeviceAuthExpiry
	EffectStopDisplayTimer
	EffectStopPoll
	EffectStopDeviceAuthTimer
	EffectCancelPasscode
	EffectCancelQR
	EffectOfferRestoreOrSkip
	EffectHandoffPermanent
	EffectHandoffTemporary
)

// Effect is a typed side-effect request. ChallengeID is a short-lived
// challenge identifier only; it is never a credential.
type Effect struct {
	Kind        EffectKind
	Generation  uint64
	ChallengeID string
	Deadline    time.Time
	Restore     RestoreChoice
}
