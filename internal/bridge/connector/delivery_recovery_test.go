package connector

import (
	"errors"
	"fmt"
	"testing"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestSuppressedMatrixRefusalDoesNotCommitUnmappedMessage(t *testing.T) {
	for _, refusal := range []error{mautrix.MForbidden, mautrix.MNotFound, mautrix.MBadJSON, mautrix.MInvalidParam, mautrix.MBadState} {
		t.Run(refusal.Error(), func(t *testing.T) {
			kc, backend, mx := newGroupCreationFramework(t)
			_, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
			if err != nil {
				t.Fatal(err)
			}
			mx.failMessage = "refused"
			mx.failMessageError = refusal
			kc.queue = kc.login.QueueRemoteEvent
			message := events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "refused"}
			if kc.handleEvent(backend, message) || len(backend.committed()) != 0 {
				t.Fatal("SDK-suppressed Matrix refusal committed an unmapped source message")
			}
			mx.failMessage = ""
			if !kc.handleEvent(backend, message) || len(backend.committed()) != 1 {
				t.Fatal("restored delivery did not advance progress")
			}
			if !kc.handleEvent(backend, message) {
				t.Fatal("existing mapping did not authorize duplicate reconciliation")
			}
			parts, err := kc.login.Bridge.DB.Message.GetAllPartsByID(t.Context(), kc.login.ID, makeMessageID(5000, 103))
			if err != nil || len(parts) != 1 || len(mx.messageBodies) != 1 {
				t.Fatal("replay duplicated the Matrix event")
			}
		})
	}
}

func TestSuppressedMatrixRefusalDoesNotAdvanceHistoryJournal(t *testing.T) {
	kc, source := newHistoryTest(t)
	mx := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	mx.failMessage = "refused history"
	mx.failMessageError = mautrix.MForbidden
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: mx.failMessage}}, Next: 103, Complete: true}}
	kc.queue = kc.login.QueueRemoteEvent
	_, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false)
	if err == nil {
		t.Fatal("SDK-suppressed refusal reported history completion")
	}
	progress, found, err := kc.loadGroupHistory(t.Context(), "kakao:group-history:1000:5000")
	if err != nil || !found || progress.After != 100 || progress.Done {
		t.Fatal("refused history advanced its durable journal")
	}
	mx.failMessage = ""
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "refused history"}}, Next: 103, Complete: true}}
	if done, err := kc.BackfillGroup(t.Context(), "!selected:test", 0, 0, 0, true); err != nil || !done {
		t.Fatal("restored historical delivery could not explicitly resume")
	}
	if len(mx.messageBodies) != 1 {
		t.Fatal("history resume duplicated the send")
	}
}

func TestSuppressedMultipartRefusalRetainsProgressAndResumesMissingParts(t *testing.T) {
	kc, backend, mx := newGroupCreationFramework(t)
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	kc.queue = kc.login.QueueRemoteEvent
	message := events.MiniTextMessage{TextMessage: events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "firstsecond"}, Parts: []events.MiniTextPart{{Text: "first"}, {Text: "second"}}}
	mx.failMessage = "second"
	mx.failMessageError = mautrix.MForbidden
	if kc.handleEvent(backend, message) || len(backend.committed()) != 0 {
		t.Fatal("partially mapped refused message committed source progress")
	}
	parts, err := kc.login.Bridge.DB.Message.GetAllPartsByID(t.Context(), kc.login.ID, makeMessageID(5000, 103))
	if err != nil || len(parts) != 1 || len(mx.messageBodies) != 1 {
		t.Fatal("failure did not retain exactly the successful first part")
	}
	mx.failMessage = ""
	if !kc.handleEvent(backend, message) || len(backend.committed()) != 1 {
		t.Fatal("missing part recovery did not commit the complete message")
	}
	if !kc.handleEvent(backend, message) {
		t.Fatal("complete replay was rejected")
	}
	parts, err = kc.login.Bridge.DB.Message.GetAllPartsByID(t.Context(), kc.login.ID, makeMessageID(5000, 103))
	if err != nil || len(parts) != 2 || len(mx.messageBodies) != 2 || mx.messageBodies[0] != "first" || mx.messageBodies[1] != "second" {
		t.Fatal("recovery lost, reordered or duplicated a part")
	}
}

func TestCatchUpRetriesOnlyUnconfirmedMatrixDelivery(t *testing.T) {
	if !retryableRecoveryError(bootstrapFailure{stage: "catch-up", err: fmt.Errorf("catch-up event: %w", errDeliveryNotConfirmed)}) {
		t.Fatal("unconfirmed catch-up delivery is not retried")
	}
	for _, err := range []error{client.ErrClosed, client.ErrGapUnresolved, errors.New("catch up chat: synthetic source failure")} {
		if retryableRecoveryError(bootstrapFailure{stage: "catch-up", err: err}) {
			t.Fatalf("source-side catch-up failure became retryable: %v", err)
		}
	}
}
