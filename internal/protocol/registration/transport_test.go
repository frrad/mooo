package registration

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeTransport struct {
	generate func(context.Context, GenerateRequest) (GenerateResponse, error)
	poll     func(context.Context, PollRequest) (PollResponse, error)
	cancel   func(context.Context, CancelRequest) error
}

func (f fakeTransport) Generate(ctx context.Context, request GenerateRequest) (GenerateResponse, error) {
	return f.generate(ctx, request)
}

func (f fakeTransport) Poll(ctx context.Context, request PollRequest) (PollResponse, error) {
	return f.poll(ctx, request)
}

func (f fakeTransport) Cancel(ctx context.Context, request CancelRequest) error {
	return f.cancel(ctx, request)
}

func TestCoordinatorDispatchesQRGenerationAndPollWithoutWireAssumptions(t *testing.T) {
	deadline := testDeadline
	var gotGenerate GenerateRequest
	var gotPoll PollRequest
	transport := fakeTransport{
		generate: func(_ context.Context, request GenerateRequest) (GenerateResponse, error) {
			gotGenerate = request
			return GenerateResponse{
				Generation:   request.Generation,
				QRPayload:    "synthetic://qr?id=synthetic-qr-id&opaque=payload",
				DisplayUntil: deadline,
			}, nil
		},
		poll: func(_ context.Context, request PollRequest) (PollResponse, error) {
			gotPoll = request
			return PollResponse{Generation: request.Generation, Outcome: OutcomePending}, nil
		},
	}
	c := NewCoordinator(MethodQR, ModePermanent, transport)

	effects, err := c.Apply(Start{})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Kind != EffectGenerateQR {
		t.Fatalf("start effects = %#v", effects)
	}
	dispatch, err := c.DispatchTransport(context.Background(), effects[0])
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.QRPayload != "synthetic://qr?id=synthetic-qr-id&opaque=payload" {
		t.Fatalf("QR payload = %q", dispatch.QRPayload)
	}
	effects = dispatch.Effects
	wantGenerate := GenerateRequest{Generation: 1, Method: MethodQR, Mode: ModePermanent}
	if !reflect.DeepEqual(gotGenerate, wantGenerate) {
		t.Fatalf("generate request = %#v, want %#v", gotGenerate, wantGenerate)
	}
	if c.State().Phase != PhaseAwaitingApproval || c.State().Challenge.QRID != "synthetic-qr-id" {
		t.Fatalf("after generation = %#v", c.State())
	}
	if c.State().Challenge.Passcode != "" {
		t.Fatalf("QR payload leaked into reducer challenge: %#v", c.State().Challenge)
	}

	var pollEffect Effect
	for _, effect := range effects {
		if effect.Kind == EffectSchedulePoll {
			pollEffect = effect
		}
	}
	if pollEffect.Kind != EffectSchedulePoll {
		t.Fatalf("generation effects = %#v", effects)
	}
	// A timer owner would later emit PollDue; the coordinator does not run it.
	effects, err = c.Apply(PollDue{Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Kind != EffectPollQR {
		t.Fatalf("poll effects = %#v", effects)
	}
	dispatch, err = c.DispatchTransport(context.Background(), effects[0])
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.QRPayload != "" {
		t.Fatalf("poll retained QR payload = %q", dispatch.QRPayload)
	}
	wantPoll := PollRequest{Generation: 1, Method: MethodQR, ChallengeID: "synthetic-qr-id"}
	if !reflect.DeepEqual(gotPoll, wantPoll) {
		t.Fatalf("poll request = %#v, want %#v", gotPoll, wantPoll)
	}
	if c.State().Phase != PhaseAwaitingApproval || c.State().Outcome != OutcomePending {
		t.Fatalf("after pending poll = %#v", c.State())
	}
}

func TestCoordinatorDispatchesPasscodeGenerationAndMapsResult(t *testing.T) {
	transport := fakeTransport{
		generate: func(_ context.Context, request GenerateRequest) (GenerateResponse, error) {
			if request.Method != MethodPasscode || request.Mode != ModeTemporary {
				t.Fatalf("unexpected request: %#v", request)
			}
			return GenerateResponse{Generation: request.Generation, Passcode: "AB12", DisplayUntil: testDeadline}, nil
		},
	}
	c := NewCoordinator(MethodPasscode, ModeTemporary, transport)
	effects, err := c.Apply(Start{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := c.DispatchTransport(context.Background(), effects[0])
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.QRPayload != "" {
		t.Fatalf("passcode dispatch returned QR payload = %q", dispatch.QRPayload)
	}
	if state := c.State(); state.Phase != PhaseAwaitingApproval || state.Challenge.Passcode != "AB12" || !state.Timers.Poll {
		t.Fatalf("passcode state = %#v", state)
	}
}

func TestCoordinatorRejectsMalformedQRPresentationPayloadBeforeReducer(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty", payload: ""},
		{name: "invalid UTF-8", payload: string([]byte{0xff, 0xfe})},
		{name: "oversized", payload: strings.Repeat("x", MaxQRPayloadBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := fakeTransport{
				generate: func(_ context.Context, request GenerateRequest) (GenerateResponse, error) {
					return GenerateResponse{
						Generation:   request.Generation,
						QRPayload:    test.payload,
						DisplayUntil: testDeadline,
					}, nil
				},
			}
			c := NewCoordinator(MethodQR, ModeTemporary, transport)
			effects, err := c.Apply(Start{})
			if err != nil {
				t.Fatal(err)
			}
			before := c.State()
			_, err = c.DispatchTransport(context.Background(), effects[0])
			if !errors.Is(err, ErrInvalidQRPayload) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidQRPayload)
			}
			if err.Error() != ErrInvalidQRPayload.Error() {
				t.Fatalf("error was not static/redacted: %q", err)
			}
			if c.State() != before {
				t.Fatalf("invalid payload changed reducer state: %#v", c.State())
			}
		})
	}
}

func TestCoordinatorRejectsNonTransportEffectsAndMismatchedResults(t *testing.T) {
	transport := fakeTransport{
		generate: func(_ context.Context, request GenerateRequest) (GenerateResponse, error) {
			return GenerateResponse{Generation: request.Generation + 1, DisplayUntil: testDeadline}, nil
		},
	}
	c := NewCoordinator(MethodQR, ModeTemporary, transport)
	effects, err := c.Apply(Start{})
	if err != nil {
		t.Fatal(err)
	}
	before := c.State()
	if _, err := c.DispatchTransport(context.Background(), Effect{Kind: EffectSchedulePoll, Generation: 1}); !errors.Is(err, ErrNotTransportEffect) {
		t.Fatalf("timer effect error = %v, want %v", err, ErrNotTransportEffect)
	}
	if c.State() != before {
		t.Fatalf("timer effect changed state: %#v", c.State())
	}
	if _, err := c.DispatchTransport(context.Background(), effects[0]); !errors.Is(err, ErrTransportGeneration) {
		t.Fatalf("mismatched result error = %v, want %v", err, ErrTransportGeneration)
	}
	if c.State() != before {
		t.Fatalf("mismatched result changed state: %#v", c.State())
	}
}

func TestCoordinatorCancellationIsTypedAndRemoteFailureDoesNotReviveState(t *testing.T) {
	var got CancelRequest
	remoteErr := errors.New("synthetic cancellation failure")
	transport := fakeTransport{
		cancel: func(_ context.Context, request CancelRequest) error {
			got = request
			return remoteErr
		},
	}
	c := NewCoordinator(MethodQR, ModeTemporary, transport)
	effects, err := c.Apply(Start{})
	if err != nil {
		t.Fatal(err)
	}
	// Use a generated synthetic challenge so cancellation includes its
	// transient identifier, as the reducer requires.
	c.transport = fakeTransport{
		generate: func(_ context.Context, request GenerateRequest) (GenerateResponse, error) {
			return GenerateResponse{
				Generation:   request.Generation,
				QRPayload:    "synthetic://cancel?id=synthetic-cancel-id",
				DisplayUntil: testDeadline,
			}, nil
		},
		cancel: transport.cancel,
	}
	if _, err = c.DispatchTransport(context.Background(), effects[0]); err != nil {
		t.Fatal(err)
	}
	effects, err = c.Apply(Cancel{Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	var cancelEffect Effect
	for _, effect := range effects {
		if effect.Kind == EffectCancelQR {
			cancelEffect = effect
		}
	}
	if cancelEffect.Kind != EffectCancelQR {
		t.Fatalf("cancel effects = %#v", effects)
	}
	if _, err := c.DispatchTransport(context.Background(), cancelEffect); !errors.Is(err, remoteErr) {
		t.Fatalf("cancel error = %v, want %v", err, remoteErr)
	}
	want := CancelRequest{Generation: 1, Method: MethodQR, ChallengeID: "synthetic-cancel-id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cancel request = %#v, want %#v", got, want)
	}
	if c.State().Phase != PhaseCancelled || c.State().Challenge != (Challenge{}) {
		t.Fatalf("cancel failure revived or retained state: %#v", c.State())
	}
}

func TestCoordinatorNoTransportAndNilContextAreSafe(t *testing.T) {
	c := NewCoordinator(MethodQR, ModeTemporary, nil)
	effects, err := c.Apply(Start{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DispatchTransport(context.Background(), effects[0]); !errors.Is(err, ErrNoTransport) {
		t.Fatalf("missing transport error = %v, want %v", err, ErrNoTransport)
	}
}
