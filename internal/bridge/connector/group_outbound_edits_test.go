package connector

import (
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

func (f *fakeKakao) ModifyMessage(_ context.Context, request chat.ModifyRequest) (int64, error) {
	f.modifies = append(f.modifies, request)
	return f.modifyRevision, f.modifyErr
}

func (f *fakeKakao) DeleteMessage(_ context.Context, request chat.DeleteRequest) error {
	f.deletes = append(f.deletes, request)
	return f.deleteErr
}

func ownTarget(logID int64, typ int32, sent time.Time) *database.Message {
	return &database.Message{
		ID: makeMessageID(testChatID, logID), SenderID: makeUserID(testSelfID), Timestamp: sent,
		Metadata: newKakaoMessageMetadata(testChatID, logID, testSelfID, typ, "synthetic original", 0),
	}
}

func matrixEdit(target *database.Message, body string) *bridgev2.MatrixEdit {
	msg := &bridgev2.MatrixEdit{EditTarget: target}
	msg.Event = &event.Event{ID: id.EventID("$edit-" + body)}
	msg.Content = &event.MessageEventContent{MsgType: event.MsgText, Body: body}
	msg.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, "1000")}}
	return msg
}

func matrixRemove(target *database.Message, eventID string) *bridgev2.MatrixMessageRemove {
	msg := &bridgev2.MatrixMessageRemove{TargetMessage: target}
	msg.Event = &event.Event{ID: id.EventID(eventID)}
	msg.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, "1000")}}
	return msg
}

func countReservations(kc *KakaoClient) *int {
	n := 0
	seen := map[id.EventID]bool{}
	kc.reserveOutbound = func(_ context.Context, eventID id.EventID) (bool, error) {
		n++
		if seen[eventID] {
			return false, nil
		}
		seen[eventID] = true
		return true, nil
	}
	return &n
}

// requireStatus checks a handler's message status and reports whether it
// asks for a notice.
func requireStatus(t *testing.T, err error, want event.MessageStatus, certain bool) (sendNotice bool) {
	t.Helper()
	var status bridgev2.MessageStatus
	if !errors.As(err, &status) || status.Status != want || status.IsCertain != certain {
		t.Fatalf("err = %v status = %+v, want %s certain=%v", err, status, want, certain)
	}
	return status.SendNotice
}

func TestMatrixEditOfOwnTextIsSentOnceAndRecordsTheRevision(t *testing.T) {
	fake := &fakeKakao{modifyRevision: 1}
	kc := connectedClient(t, fake)
	reservations := countReservations(kc)
	target := ownTarget(201, chat.TextType, time.Now().Add(-time.Hour))
	if err := kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic matrix edit")); err != nil {
		t.Fatal(err)
	}
	if *reservations != 1 || len(fake.modifies) != 1 {
		t.Fatalf("reservations=%d sends=%d", *reservations, len(fake.modifies))
	}
	if got := fake.modifies[0]; got != (chat.ModifyRequest{ChatID: testChatID, LogID: 201, Type: chat.TextType, Message: "synthetic matrix edit", Extra: "{}"}) {
		t.Fatalf("MODIFYMSG = %+v", got)
	}
	meta := target.Metadata.(*KakaoMessageMetadata)
	if meta.Revision != 1 || meta.Preview != "synthetic matrix edit" {
		t.Fatalf("metadata = %+v", meta)
	}
	// A redelivered copy of the same Matrix edit is never sent again, whether
	// the stored text already matches or only the reservation remains.
	_ = kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic matrix edit"))
	target.Metadata.(*KakaoMessageMetadata).Preview = "synthetic stale preview"
	if err := kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic matrix edit")); err == nil || len(fake.modifies) != 1 {
		t.Fatalf("duplicate edit err=%v sends=%d", err, len(fake.modifies))
	}
}

func TestMatrixEditIsRejectedBeforeAnySendWhenKakaoWouldRefuse(t *testing.T) {
	other := ownTarget(201, chat.TextType, time.Now())
	other.Metadata.(*KakaoMessageMetadata).AuthorID = 2000
	cases := []struct {
		label  string
		target *database.Message
	}{
		{"another member's message", other},
		{"older than 24 hours", ownTarget(201, chat.TextType, time.Now().Add(-25*time.Hour))},
		{"photo", ownTarget(201, media.PhotoType, time.Now())},
		{"reply", ownTarget(201, chat.ReplyType, time.Now())},
		{"deleted", ownTarget(201, chat.TextType|messagetype.DeletedAllChatTypeFlag, time.Now())},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			fake := &fakeKakao{modifyRevision: 1}
			kc := connectedClient(t, fake)
			reservations := countReservations(kc)
			notice := requireStatus(t, kc.HandleMatrixEdit(context.Background(), matrixEdit(tc.target, "synthetic matrix edit")), event.MessageStatusFail, true)
			if !notice || *reservations != 0 || len(fake.modifies) != 0 {
				t.Fatalf("notice=%v reservations=%d sends=%d", notice, *reservations, len(fake.modifies))
			}
		})
	}
}

func TestMatrixEditOutcomes(t *testing.T) {
	refused := &fakeKakao{modifyErr: client.StatusError{Command: chat.ModifyCommand, Status: -1}}
	kc := connectedClient(t, refused)
	countReservations(kc)
	requireStatus(t, kc.HandleMatrixEdit(context.Background(), matrixEdit(ownTarget(201, chat.TextType, time.Now()), "a")), event.MessageStatusFail, true)

	lost := &fakeKakao{modifyErr: errors.New("synthetic disconnect")}
	kc = connectedClient(t, lost)
	countReservations(kc)
	requireStatus(t, kc.HandleMatrixEdit(context.Background(), matrixEdit(ownTarget(201, chat.TextType, time.Now()), "b")), event.MessageStatusFail, false)
	if len(lost.modifies) != 1 {
		t.Fatalf("ambiguous edit was retried: %d sends", len(lost.modifies))
	}

	same := &fakeKakao{}
	kc = connectedClient(t, same)
	countReservations(kc)
	if err := kc.HandleMatrixEdit(context.Background(), matrixEdit(ownTarget(201, chat.TextType, time.Now()), "synthetic original")); err != nil || len(same.modifies) != 0 {
		t.Fatalf("unchanged edit err=%v sends=%d", err, len(same.modifies))
	}
}

func TestMatrixRedactionDeletesOwnMessageForEveryoneOnce(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	reservations := countReservations(kc)
	target := ownTarget(203, media.PhotoType, time.Now().Add(-time.Hour))
	if err := kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(target, "$redact-1")); err != nil {
		t.Fatal(err)
	}
	if *reservations != 1 || len(fake.deletes) != 1 || fake.deletes[0] != (chat.DeleteRequest{ChatID: testChatID, LogID: 203}) {
		t.Fatalf("reservations=%d deletes=%+v", *reservations, fake.deletes)
	}
	if err := kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(target, "$redact-1")); err == nil || len(fake.deletes) != 1 {
		t.Fatalf("duplicate redaction err=%v deletes=%d", err, len(fake.deletes))
	}
}

func TestMatrixRedactionOutcomes(t *testing.T) {
	other := ownTarget(203, chat.TextType, time.Now())
	other.Metadata.(*KakaoMessageMetadata).AuthorID = 2000
	before := []struct {
		label  string
		target *database.Message
	}{
		{"another member's message", other},
		{"older than 24 hours", ownTarget(203, chat.TextType, time.Now().Add(-25*time.Hour))},
	}
	for _, tc := range before {
		fake := &fakeKakao{}
		kc := connectedClient(t, fake)
		reservations := countReservations(kc)
		requireStatus(t, kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(tc.target, "$r-"+tc.label)), event.MessageStatusFail, true)
		if *reservations != 0 || len(fake.deletes) != 0 {
			t.Fatalf("%s: reservations=%d deletes=%d", tc.label, *reservations, len(fake.deletes))
		}
	}
	// A deleted placeholder needs no source request.
	placeholder := &fakeKakao{}
	kc := connectedClient(t, placeholder)
	countReservations(kc)
	if err := kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(ownTarget(203, chat.TextType|messagetype.DeletedAllChatTypeFlag, time.Now()), "$r-placeholder")); err != nil || len(placeholder.deletes) != 0 {
		t.Fatalf("placeholder err=%v deletes=%d", err, len(placeholder.deletes))
	}
	for _, tc := range []struct {
		status  int
		want    event.MessageStatus
		certain bool
		ok      bool
	}{
		{chat.StatusDeleteTimeExpired, event.MessageStatusFail, true, false},
		{chat.StatusDeleteTypeNotAllowed, event.MessageStatusFail, true, false},
		{chat.StatusAlreadyDeleted, "", true, true},
	} {
		fake := &fakeKakao{deleteErr: client.StatusError{Command: chat.DeleteCommand, Status: int32(tc.status)}}
		kc := connectedClient(t, fake)
		countReservations(kc)
		err := kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(ownTarget(203, chat.TextType, time.Now()), "$r-status"))
		if tc.ok {
			if err != nil {
				t.Fatalf("status %d err = %v", tc.status, err)
			}
			continue
		}
		requireStatus(t, err, tc.want, tc.certain)
	}
	lost := &fakeKakao{deleteErr: errors.New("synthetic disconnect")}
	kc = connectedClient(t, lost)
	countReservations(kc)
	requireStatus(t, kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(ownTarget(203, chat.TextType, time.Now()), "$r-lost")), event.MessageStatusFail, false)
}

func TestEditAndDeleteCapabilitiesCarryKakaoLimits(t *testing.T) {
	caps := (&KakaoClient{}).GetCapabilities(context.Background(), nil)
	if caps.Edit != event.CapLevelPartialSupport || caps.EditMaxAge == nil || caps.EditMaxAge.Duration != 24*time.Hour ||
		caps.Delete != event.CapLevelPartialSupport || caps.DeleteMaxAge == nil || caps.DeleteMaxAge.Duration != 24*time.Hour {
		t.Fatalf("caps edit=%v/%v delete=%v/%v", caps.Edit, caps.EditMaxAge, caps.Delete, caps.DeleteMaxAge)
	}
}

// Regression (owned acceptance, 2026-10-10): a message sent and then deleted
// from Matrix lost its mapping with the redaction; outbound sends do not move
// the delivery cursor, so catch-up later delivered the deleted-flagged log
// and the bridge posted a "deleted" placeholder the user never saw before.
func TestCatchUpOfAMessageDeletedFromMatrixPostsNothing(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	kc.client = backend
	countReservations(kc)
	target := ownTarget(205, chat.TextType, time.Now())
	if err := kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(target, "$r-own")); err != nil {
		t.Fatal(err)
	}
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResultSuccess
	}
	if !kc.handleEvent(backend, events.DeletedMessage{ChatID: testChatID, LogID: 205, AuthorID: testSelfID, BaseType: chat.TextType}) {
		t.Fatal("deleted log was not handled")
	}
	if queued != 0 || len(backend.committed()) != 1 {
		t.Fatalf("queued=%d commits=%d", queued, len(backend.committed()))
	}
}
