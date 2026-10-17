package connector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

func (f *fakeKakao) EditSource(_ context.Context, chatID, logID int64) (chat.EditSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editSourceRequests = append(f.editSourceRequests, logID)
	if f.editSourceErr != nil {
		return chat.EditSource{}, f.editSourceErr
	}
	source, ok := f.editSources[logID]
	if !ok || chatID != testChatID {
		return chat.EditSource{}, chat.ErrEditSourceMissing
	}
	return source, nil
}

// storedOwnPart stores one bridged part of an own message and returns a copy
// as the framework would hand it to HandleMatrixEdit.
func storedOwnPart(kc *KakaoClient, logID int64, typ int32, partID networkid.PartID, preview string) *database.Message {
	part := &database.Message{
		ID: makeMessageID(testChatID, logID), PartID: partID, Room: makePortalKey(testChatID, kc.login.ID),
		SenderID: makeUserID(testSelfID), Timestamp: time.Now().Add(-time.Hour),
		Metadata: newKakaoMessageMetadata(testChatID, logID, testSelfID, typ, preview, 0),
	}
	kc.store.(*memStore).putMessage(part)
	return copyMessage(part)
}

func storedPartMetadata(t *testing.T, kc *KakaoClient, logID int64, partID networkid.PartID) *KakaoMessageMetadata {
	t.Helper()
	parts, err := kc.store.messageParts(context.Background(), kc.login.ID, makeMessageID(testChatID, logID))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if part.PartID == partID {
			return part.Metadata.(*KakaoMessageMetadata)
		}
	}
	t.Fatalf("part %q not stored", partID)
	return nil
}

func requireJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("extra %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("extra = %s, want %s", got, want)
	}
}

// The Mac client lets its user edit the text of their own sticker sent with
// text (executed), and keeps the sticker by copying the original sticker keys
// into MODIFYMSG extra (static). A Matrix edit of the bridged text part reads
// the current log and sends the same request; both parts take the new text as
// their reply preview.
func TestMatrixEditOfOwnStickerTextKeepsTheSticker(t *testing.T) {
	fake := &fakeKakao{modifyRevision: 2, editSources: map[int64]chat.EditSource{401: {
		LogID: 401, AuthorID: testSelfID, Type: messagetype.Sticker, Message: "synthetic caption",
		Attachment: `{"path":"synthetic/emot_001.png","emoticonItemPath":"synthetic/emot_001.png","name":"(Emoticons)","type":"image/png"}`,
	}}}
	kc := connectedClient(t, fake)
	reservations := countReservations(kc)
	storedOwnPart(kc, 401, messagetype.Sticker, "", "synthetic caption")
	target := storedOwnPart(kc, 401, messagetype.Sticker, stickerTextPartID, "synthetic caption")
	if err := kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic matrix edit")); err != nil {
		t.Fatal(err)
	}
	if *reservations != 1 || len(fake.modifies) != 1 || len(fake.editSourceRequests) != 1 || fake.editSourceRequests[0] != 401 {
		t.Fatalf("reservations=%d sends=%d reads=%v", *reservations, len(fake.modifies), fake.editSourceRequests)
	}
	got := fake.modifies[0]
	if got.ChatID != testChatID || got.LogID != 401 || got.Type != messagetype.Sticker || got.Message != "synthetic matrix edit" {
		t.Fatalf("MODIFYMSG = %+v", got)
	}
	requireJSON(t, got.Extra, `{"path":"synthetic/emot_001.png","name":"(Emoticons)"}`)
	if meta := target.Metadata.(*KakaoMessageMetadata); meta.Revision != 2 || meta.Preview != "synthetic matrix edit" {
		t.Fatalf("text part metadata = %+v", meta)
	}
	if meta := storedPartMetadata(t, kc, 401, ""); meta.Revision != 2 || meta.Preview != "synthetic matrix edit" {
		t.Fatalf("sticker part metadata = %+v", meta)
	}
}

// A reply that is not attachment-only is editable on the Mac (executed); the
// edit keeps the reply source and any sticker by copying the src_* and
// attach_* keys (static).
func TestMatrixEditOfOwnStickerReplyKeepsSourceAndSticker(t *testing.T) {
	fake := &fakeKakao{modifyRevision: 1, editSources: map[int64]chat.EditSource{402: {
		LogID: 402, AuthorID: testSelfID, Type: messagetype.Reply, Message: "synthetic reply",
		Attachment: `{"src_logId":401,"src_userId":2000,"src_type":12,"src_message":"synthetic caption","attach_type":12,"attach_content":{"name":"(Emoticons)","type":"image/png","path":"synthetic/emot_002.png"},"emoticonItemPath":"synthetic/emot_002.png","src_spoilers":[]}`,
	}}}
	kc := connectedClient(t, fake)
	countReservations(kc)
	target := storedOwnPart(kc, 402, messagetype.Reply, "", "synthetic reply")
	storedOwnPart(kc, 402, messagetype.Reply, replyStickerPartID, "synthetic reply")
	if err := kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic reply edit")); err != nil {
		t.Fatal(err)
	}
	if len(fake.modifies) != 1 || fake.modifies[0].Type != messagetype.Reply || fake.modifies[0].Message != "synthetic reply edit" {
		t.Fatalf("MODIFYMSG = %+v", fake.modifies)
	}
	requireJSON(t, fake.modifies[0].Extra, `{"src_logId":401,"src_userId":2000,"src_type":12,"src_message":"synthetic caption","attach_type":12,"attach_content":{"name":"(Emoticons)","type":"image/png","path":"synthetic/emot_002.png"}}`)
	if meta := storedPartMetadata(t, kc, 402, replyStickerPartID); meta.Revision != 1 || meta.Preview != "synthetic reply edit" {
		t.Fatalf("reply sticker part metadata = %+v", meta)
	}
}

func TestMatrixEditOfStickerOrReplyIsRefusedBeforeAnySend(t *testing.T) {
	stickerAttachment := `{"path":"synthetic/emot_001.png","name":"(Emoticons)"}`
	cases := []struct {
		label   string
		typ     int32
		partID  networkid.PartID
		source  *chat.EditSource
		readErr error
		reads   int
	}{
		{label: "the sticker part itself", typ: messagetype.Sticker, partID: "", reads: 0},
		{label: "a reply's sticker part", typ: messagetype.Reply, partID: replyStickerPartID, reads: 0},
		{label: "attachment-only sticker reply", typ: messagetype.Reply, partID: "", reads: 1, source: &chat.EditSource{
			LogID: 403, AuthorID: testSelfID, Type: messagetype.Reply, Message: "(Emoticons)",
			Attachment: `{"src_logId":400,"src_userId":2000,"src_type":1,"attach_only":true,"attach_type":12,"attach_content":{"path":"synthetic/emot_002.png"}}`,
		}},
		{label: "target no longer returned", typ: messagetype.Sticker, partID: stickerTextPartID, reads: 1},
		{label: "target has another author", typ: messagetype.Sticker, partID: stickerTextPartID, reads: 1, source: &chat.EditSource{
			LogID: 403, AuthorID: 2000, Type: messagetype.Sticker, Message: "synthetic caption", Attachment: stickerAttachment,
		}},
		{label: "target has another type", typ: messagetype.Sticker, partID: stickerTextPartID, reads: 1, source: &chat.EditSource{
			LogID: 403, AuthorID: testSelfID, Type: messagetype.Text, Message: "synthetic caption",
		}},
		{label: "target sticker has no text", typ: messagetype.Sticker, partID: stickerTextPartID, reads: 1, source: &chat.EditSource{
			LogID: 403, AuthorID: testSelfID, Type: messagetype.Sticker, Attachment: stickerAttachment,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			fake := &fakeKakao{modifyRevision: 1, editSources: map[int64]chat.EditSource{}}
			if tc.source != nil {
				fake.editSources[403] = *tc.source
			}
			kc := connectedClient(t, fake)
			reservations := countReservations(kc)
			target := storedOwnPart(kc, 403, tc.typ, tc.partID, "synthetic caption")
			notice := requireStatus(t, kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic matrix edit")), event.MessageStatusFail, true)
			if !notice || *reservations != 0 || len(fake.modifies) != 0 || len(fake.editSourceRequests) != tc.reads {
				t.Fatalf("notice=%v reservations=%d sends=%d reads=%d", notice, *reservations, len(fake.modifies), len(fake.editSourceRequests))
			}
		})
	}
}

// Nothing has been sent when reading the target fails, so the failure is
// certain and the edit is not sent.
func TestMatrixStickerEditReadFailureIsCertainAndSendsNothing(t *testing.T) {
	fake := &fakeKakao{modifyRevision: 1, editSourceErr: errors.New("synthetic disconnect")}
	kc := connectedClient(t, fake)
	countReservations(kc)
	target := storedOwnPart(kc, 404, messagetype.Sticker, stickerTextPartID, "synthetic caption")
	requireStatus(t, kc.HandleMatrixEdit(context.Background(), matrixEdit(target, "synthetic matrix edit")), event.MessageStatusFail, true)
	if len(fake.modifies) != 0 {
		t.Fatalf("sends = %d", len(fake.modifies))
	}
}
