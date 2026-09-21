package registration

import (
	"errors"
	"testing"
	"time"
)

var testDeadline = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func reduceOK(t *testing.T, s State, event Event) (State, []Effect) {
	t.Helper()
	next, effects, err := Reduce(s, event)
	if err != nil {
		t.Fatalf("Reduce(%T) error: %v", event, err)
	}
	return next, effects
}

func effectKinds(effects []Effect) []EffectKind {
	result := make([]EffectKind, len(effects))
	for i := range effects {
		result[i] = effects[i].Kind
	}
	return result
}

func assertKinds(t *testing.T, effects []Effect, want ...EffectKind) {
	t.Helper()
	got := effectKinds(effects)
	if len(got) != len(want) {
		t.Fatalf("effect kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("effect kinds = %v, want %v", got, want)
		}
	}
}

func passcodeAwaiting(t *testing.T, mode Mode) State {
	t.Helper()
	s, _ := reduceOK(t, New(MethodPasscode, mode), Start{})
	s, _ = reduceOK(t, s, PasscodeGenerated{Generation: 1, Code: "AB12", DisplayUntil: testDeadline})
	return s
}

func qrAwaiting(t *testing.T, mode Mode) State {
	t.Helper()
	s, _ := reduceOK(t, New(MethodQR, mode), Start{})
	s, _ = reduceOK(t, s, QRGenerated{Generation: 1, QRID: "qr-synthetic-1", DisplayUntil: testDeadline})
	return s
}

func TestConformancePasscodePendingToApprovedStopsPollingAndExpiry(t *testing.T) {
	s := passcodeAwaiting(t, ModePermanent)
	var effects []Effect
	s, effects = reduceOK(t, s, PollDue{Generation: 1})
	assertKinds(t, effects, EffectPollPasscode)
	s, effects = reduceOK(t, s, PollResult{Generation: 1, Outcome: OutcomePending})
	assertKinds(t, effects, EffectSchedulePoll)
	s, effects = reduceOK(t, s, PollResult{Generation: 1, Outcome: OutcomeApproved})
	if s.Phase != PhaseAwaitingRestore || s.Timers != (Timers{}) {
		t.Fatalf("approved state = %#v", s)
	}
	assertKinds(t, effects, EffectStopDisplayTimer, EffectStopPoll, EffectOfferRestoreOrSkip)
}

func TestConformancePasscodeDisplayExpiryPreventsPoll(t *testing.T) {
	s := passcodeAwaiting(t, ModeTemporary)
	var effects []Effect
	s, effects = reduceOK(t, s, DisplayExpired{Generation: 1})
	if s.Phase != PhaseExpired || s.Outcome != OutcomeExpired || s.Timers != (Timers{}) {
		t.Fatalf("expired state = %#v", s)
	}
	assertKinds(t, effects, EffectStopDisplayTimer, EffectStopPoll, EffectCancelPasscode)
	_, effects, err := Reduce(s, PollDue{Generation: 1})
	if !errors.Is(err, ErrInvalidTransition) || len(effects) != 0 {
		t.Fatalf("poll after expiry: err=%v effects=%v", err, effects)
	}
}

func TestConformanceQRPendingToDeviceAuthorizationToAuthExpiry(t *testing.T) {
	s := qrAwaiting(t, ModeTemporary)
	var effects []Effect
	s, effects = reduceOK(t, s, PollResult{Generation: 1, Outcome: OutcomePending})
	assertKinds(t, effects, EffectSchedulePoll)
	s, effects = reduceOK(t, s, DeviceAuthorizationRequired{Generation: 1, Code: "A1B2", ExpiresAt: testDeadline})
	if s.Phase != PhaseAwaitingDeviceAuthorization || !s.Timers.Display || !s.Timers.Poll || !s.Timers.DeviceAuth {
		t.Fatalf("device authorization state = %#v", s)
	}
	assertKinds(t, effects, EffectScheduleDeviceAuthExpiry, EffectSchedulePoll)
	s, effects = reduceOK(t, s, DeviceAuthExpired{Generation: 1})
	if s.Phase != PhaseExpired || s.Challenge.QRID != "" || s.DeviceAuthCode != "" || s.Timers != (Timers{}) {
		t.Fatalf("auth-expired state = %#v", s)
	}
	assertKinds(t, effects, EffectStopDisplayTimer, EffectStopPoll, EffectStopDeviceAuthTimer, EffectCancelQR)
}

func TestConformanceQRRefreshClearsOldIdentifierBeforeGeneration(t *testing.T) {
	s := qrAwaiting(t, ModeTemporary)
	s, _ = reduceOK(t, s, DisplayExpired{Generation: 1})
	// The expired presentation is already unusable. Refresh still performs the
	// required clear-before-generate operation and advances its session token.
	s, effects := reduceOK(t, s, Refresh{Generation: 1})
	if s.Phase != PhaseGenerating || s.Generation != 2 || s.Challenge != (Challenge{}) || s.DeviceAuthCode != "" {
		t.Fatalf("refresh state = %#v", s)
	}
	assertKinds(t, effects, EffectGenerateQR)
	if effects[0].Generation != 2 {
		t.Fatalf("refresh generation effect = %#v", effects[0])
	}
}

func TestConformanceEveryKnownTerminalQROutcomeFailsClosed(t *testing.T) {
	outcomes := []Outcome{
		OutcomeRejected,
		OutcomeExpired,
		OutcomeUnsupportedDevice,
		OutcomeSuspended,
		OutcomeRestricted,
		OutcomeInvalidResponse,
		OutcomeUnknownFailure,
	}
	for _, outcome := range outcomes {
		t.Run(outcomeName(outcome), func(t *testing.T) {
			s := qrAwaiting(t, ModeTemporary)
			next, effects, err := Reduce(s, PollResult{Generation: 1, Outcome: outcome})
			if err != nil {
				t.Fatal(err)
			}
			wantPhase := PhaseFailed
			if outcome == OutcomeExpired {
				wantPhase = PhaseExpired
			}
			if next.Phase != wantPhase || next.Outcome != outcome || next.Timers != (Timers{}) {
				t.Fatalf("terminal state = %#v", next)
			}
			assertKinds(t, effects, EffectStopDisplayTimer, EffectStopPoll)
		})
	}
}

func TestConformancePermanentSuccessExposesRestoreOrSkip(t *testing.T) {
	for _, choice := range []RestoreChoice{RestoreHistory, SkipHistory} {
		t.Run(choiceName(choice), func(t *testing.T) {
			s := qrAwaiting(t, ModePermanent)
			s, _ = reduceOK(t, s, PollResult{Generation: 1, Outcome: OutcomeApproved})
			if s.Phase != PhaseAwaitingRestore {
				t.Fatalf("success phase = %v", s.Phase)
			}
			s, effects := reduceOK(t, s, RestoreSelected{Generation: 1, Choice: choice})
			if s.Phase != PhaseHandedOff || s.Restore != choice {
				t.Fatalf("handoff state = %#v", s)
			}
			assertKinds(t, effects, EffectHandoffPermanent)
			if effects[0].Restore != choice {
				t.Fatalf("handoff effect = %#v", effects[0])
			}
		})
	}
}

func TestConformanceTemporarySuccessBypassesPersistentDecision(t *testing.T) {
	s := qrAwaiting(t, ModeTemporary)
	s, effects := reduceOK(t, s, PollResult{Generation: 1, Outcome: OutcomeApproved})
	if s.Phase != PhaseHandedOff || s.Restore != RestoreNotRequested || s.Challenge != (Challenge{}) {
		t.Fatalf("temporary handoff state = %#v", s)
	}
	assertKinds(t, effects, EffectStopDisplayTimer, EffectStopPoll, EffectHandoffTemporary)
}

func TestConformanceCancellationIsIdempotentIncludingAfterExpiry(t *testing.T) {
	s := passcodeAwaiting(t, ModeTemporary)
	var effects []Effect
	s, effects = reduceOK(t, s, Cancel{Generation: 1})
	if s.Phase != PhaseCancelled || s.Challenge != (Challenge{}) {
		t.Fatalf("cancel state = %#v", s)
	}
	assertKinds(t, effects, EffectStopDisplayTimer, EffectStopPoll, EffectCancelPasscode)
	s, effects = reduceOK(t, s, Cancel{Generation: 1})
	if s.Phase != PhaseCancelled || effects != nil {
		t.Fatalf("second cancel = state %#v effects %v", s, effects)
	}

	s = passcodeAwaiting(t, ModeTemporary)
	s, _ = reduceOK(t, s, DisplayExpired{Generation: 1})
	s, effects = reduceOK(t, s, Cancel{Generation: 1})
	if s.Phase != PhaseExpired || effects != nil {
		t.Fatalf("cancel after expiry = state %#v effects %v", s, effects)
	}
}

func TestConformanceStaleEventsCannotReviveEndedSession(t *testing.T) {
	s := qrAwaiting(t, ModeTemporary)
	s, _ = reduceOK(t, s, DisplayExpired{Generation: 1})
	s, effects := reduceOK(t, s, Refresh{Generation: 1})
	if s.Generation != 2 || len(effects) != 1 {
		t.Fatalf("refresh = state %#v effects %v", s, effects)
	}
	for _, event := range []Event{
		QRGenerated{Generation: 1, QRID: "old", DisplayUntil: testDeadline},
		PollDue{Generation: 1},
		PollResult{Generation: 1, Outcome: OutcomeApproved},
		DeviceAuthExpired{Generation: 1},
	} {
		next, got, err := Reduce(s, event)
		if !errors.Is(err, ErrStaleEvent) || len(got) != 0 || next != s {
			t.Errorf("stale %T: next=%#v effects=%v err=%v", event, next, got, err)
		}
	}

	s, _ = reduceOK(t, qrAwaiting(t, ModeTemporary), Cancel{Generation: 1})
	next, got, err := Reduce(s, PollResult{Generation: 1, Outcome: OutcomeApproved})
	if err == nil || len(got) != 0 || next.Phase != PhaseCancelled {
		t.Fatalf("stale callback after cancellation: next=%#v effects=%v err=%v", next, got, err)
	}
}

func TestConformanceInvalidEventsAndIncompleteChallengesFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		state State
		event Event
		want  error
	}{
		{"bad method", New(Method(99), ModeTemporary), Start{}, ErrInvalidTransition},
		{"bad mode", New(MethodQR, Mode(99)), Start{}, ErrInvalidTransition},
		{"short passcode", func() State { s, _ := reduceOK(t, New(MethodPasscode, ModeTemporary), Start{}); return s }(), PasscodeGenerated{Generation: 1, Code: "123", DisplayUntil: testDeadline}, ErrInvalidChallenge},
		{"zero passcode deadline", func() State { s, _ := reduceOK(t, New(MethodPasscode, ModeTemporary), Start{}); return s }(), PasscodeGenerated{Generation: 1, Code: "1234"}, ErrInvalidDeadline},
		{"empty QR id", func() State { s, _ := reduceOK(t, New(MethodQR, ModeTemporary), Start{}); return s }(), QRGenerated{Generation: 1, DisplayUntil: testDeadline}, ErrInvalidChallenge},
		{"zero QR deadline", func() State { s, _ := reduceOK(t, New(MethodQR, ModeTemporary), Start{}); return s }(), QRGenerated{Generation: 1, QRID: "qr", DisplayUntil: time.Time{}}, ErrInvalidDeadline},
		{"non-four-character device auth code", qrAwaiting(t, ModeTemporary), DeviceAuthorizationRequired{Generation: 1, Code: "auth-1", ExpiresAt: testDeadline}, ErrInvalidChallenge},
		{"unknown outcome", qrAwaiting(t, ModeTemporary), PollResult{Generation: 1, Outcome: Outcome(255)}, ErrInvalidOutcome},
		{"restore before success", qrAwaiting(t, ModePermanent), RestoreSelected{Generation: 1, Choice: RestoreHistory}, ErrInvalidTransition},
		{"poll without QR id", State{Method: MethodQR, Mode: ModeTemporary, Phase: PhaseAwaitingApproval, Generation: 1, Timers: Timers{Poll: true}}, PollDue{Generation: 1}, ErrInvalidChallenge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, effects, err := Reduce(tt.state, tt.event)
			if !errors.Is(err, tt.want) || len(effects) != 0 || got != tt.state {
				t.Fatalf("got state=%#v effects=%v err=%v, want unchanged and %v", got, effects, err, tt.want)
			}
		})
	}
}

func TestStartEffectsForBothMethods(t *testing.T) {
	for _, test := range []struct {
		name   string
		method Method
		kind   EffectKind
	}{
		{"passcode", MethodPasscode, EffectGeneratePasscode},
		{"qr", MethodQR, EffectGenerateQR},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, effects := reduceOK(t, New(test.method, ModeTemporary), Start{})
			if s.Generation != 1 || s.Phase != PhaseGenerating {
				t.Fatalf("start state = %#v", s)
			}
			assertKinds(t, effects, test.kind)
		})
	}
}

func TestPollDueEffectsForBothMethods(t *testing.T) {
	for _, test := range []struct {
		name  string
		state State
		kind  EffectKind
	}{
		{"passcode", passcodeAwaiting(t, ModeTemporary), EffectPollPasscode},
		{"qr", qrAwaiting(t, ModeTemporary), EffectPollQR},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, effects := reduceOK(t, test.state, PollDue{Generation: 1})
			assertKinds(t, effects, test.kind)
		})
	}
}

func TestPollResultUnregisteredDevicePositiveAndMalformedCode(t *testing.T) {
	s := qrAwaiting(t, ModeTemporary)
	next, effects, err := Reduce(s, PollResult{
		Generation:      1,
		Outcome:         OutcomeUnregisteredDevice,
		DeviceAuthCode:  "A1B2",
		DeviceAuthUntil: testDeadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Phase != PhaseAwaitingDeviceAuthorization || next.DeviceAuthCode != "A1B2" || !next.Timers.DeviceAuth {
		t.Fatalf("device authorization state = %#v", next)
	}
	assertKinds(t, effects, EffectScheduleDeviceAuthExpiry, EffectSchedulePoll)

	_, effects, err = Reduce(s, PollResult{
		Generation:      1,
		Outcome:         OutcomeUnregisteredDevice,
		DeviceAuthCode:  "bad",
		DeviceAuthUntil: testDeadline,
	})
	if !errors.Is(err, ErrInvalidChallenge) || len(effects) != 0 {
		t.Fatalf("malformed device authorization code: err=%v effects=%v", err, effects)
	}
}

func TestPermanentPasscodeRestoreHandoff(t *testing.T) {
	s := passcodeAwaiting(t, ModePermanent)
	s, _ = reduceOK(t, s, PollResult{Generation: 1, Outcome: OutcomeApproved})
	if s.Phase != PhaseAwaitingRestore {
		t.Fatalf("passcode success phase = %v", s.Phase)
	}
	s, effects := reduceOK(t, s, RestoreSelected{Generation: 1, Choice: SkipHistory})
	if s.Phase != PhaseHandedOff || s.Restore != SkipHistory {
		t.Fatalf("passcode handoff state = %#v", s)
	}
	assertKinds(t, effects, EffectHandoffPermanent)
}

func TestCancelDuringGenerationHasNoRemoteCancel(t *testing.T) {
	s, _ := reduceOK(t, New(MethodQR, ModeTemporary), Start{})
	s, effects := reduceOK(t, s, Cancel{Generation: 1})
	if s.Phase != PhaseCancelled || len(effects) != 0 {
		t.Fatalf("cancel during generation = state %#v effects %v", s, effects)
	}
}

func TestPasscodeRejectsQROnlyOutcome(t *testing.T) {
	s := passcodeAwaiting(t, ModeTemporary)
	next, effects, err := Reduce(s, PollResult{Generation: 1, Outcome: OutcomeUnsupportedDevice})
	if !errors.Is(err, ErrInvalidOutcome) || len(effects) != 0 || next != s {
		t.Fatalf("QR-only outcome on passcode: next=%#v effects=%v err=%v", next, effects, err)
	}
}

func TestRefreshRestrictionsAndGenerationOverflow(t *testing.T) {
	qr := qrAwaiting(t, ModeTemporary)
	for _, test := range []struct {
		name  string
		state State
		event Refresh
		want  error
	}{
		{"awaiting QR", qr, Refresh{Generation: 1}, ErrInvalidTransition},
		{"expired passcode", func() State {
			s := passcodeAwaiting(t, ModeTemporary)
			s, _ = reduceOK(t, s, DisplayExpired{Generation: 1})
			return s
		}(), Refresh{Generation: 1}, ErrInvalidTransition},
		{"stale expired QR", func() State {
			s := qr
			s, _ = reduceOK(t, s, DisplayExpired{Generation: 1})
			return s
		}(), Refresh{Generation: 2}, ErrStaleEvent},
	} {
		t.Run(test.name, func(t *testing.T) {
			next, effects, err := Reduce(test.state, test.event)
			if !errors.Is(err, test.want) || len(effects) != 0 || next != test.state {
				t.Fatalf("next=%#v effects=%v err=%v, want unchanged and %v", next, effects, err, test.want)
			}
		})
	}
	max := State{Method: MethodQR, Mode: ModeTemporary, Phase: PhaseIdle, Generation: ^uint64(0)}
	next, effects, err := Reduce(max, Start{})
	if !errors.Is(err, ErrInvalidTransition) || len(effects) != 0 || next != max {
		t.Fatalf("start generation overflow: next=%#v effects=%v err=%v", next, effects, err)
	}
	max.Phase = PhaseExpired
	next, effects, err = Reduce(max, Refresh{Generation: ^uint64(0)})
	if !errors.Is(err, ErrInvalidTransition) || len(effects) != 0 || next != max {
		t.Fatalf("refresh generation overflow: next=%#v effects=%v err=%v", next, effects, err)
	}
}

type unknownEvent struct{}

func (unknownEvent) registrationEvent() {}

func TestReducerAPIRejectsNilAndUnknownEventsAndSupportsApply(t *testing.T) {
	s := New(MethodPasscode, ModeTemporary)
	next, effects, err := Reduce(s, nil)
	if !errors.Is(err, ErrInvalidTransition) || len(effects) != 0 || next != s {
		t.Fatalf("nil event: next=%#v effects=%v err=%v", next, effects, err)
	}
	next, effects, err = Reduce(s, unknownEvent{})
	if !errors.Is(err, ErrInvalidTransition) || len(effects) != 0 || next != s {
		t.Fatalf("unknown event: next=%#v effects=%v err=%v", next, effects, err)
	}
	next, effects, err = s.Apply(Start{})
	if err != nil || next.Phase != PhaseGenerating || len(effects) != 1 || effects[0].Kind != EffectGeneratePasscode {
		t.Fatalf("Apply: next=%#v effects=%v err=%v", next, effects, err)
	}
}

func outcomeName(o Outcome) string {
	return map[Outcome]string{
		OutcomeRejected:          "rejected",
		OutcomeExpired:           "expired",
		OutcomeUnsupportedDevice: "unsupported",
		OutcomeSuspended:         "suspended",
		OutcomeRestricted:        "restricted",
		OutcomeInvalidResponse:   "invalid",
		OutcomeUnknownFailure:    "unknown",
	}[o]
}

func choiceName(c RestoreChoice) string {
	if c == RestoreHistory {
		return "restore"
	}
	return "skip"
}
