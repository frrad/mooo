package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

func bridgedTextPart(chatID, logID, author int64, text string, revision int64) *database.Message {
	meta := newKakaoMessageMetadata(chatID, logID, author, chat.TextType, text, 0)
	meta.Revision = revision
	return &database.Message{ID: makeMessageID(chatID, logID), SenderID: makeUserID(author), Metadata: meta}
}

func TestInboundEditPushReplacesBridgedTextOnce(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	edit := events.MessageEdited{ChatID: testChatID, LogID: 202, AuthorID: 2000, TargetLogID: 201, TargetRevision: 1,
		Modified: events.TextMessage{ChatID: testChatID, LogID: 201, AuthorID: 2000, Message: "synthetic edited text", Revision: 1}}
	remote, ok := kc.remoteEventFor(edit).(bridgev2.RemoteEdit)
	if !ok {
		t.Fatalf("edit maps to %T", kc.remoteEventFor(edit))
	}
	if _, isMessage := remote.(bridgev2.RemoteMessage); isMessage {
		t.Fatal("an edit must not require its own mapping to commit")
	}
	if remote.GetType() != bridgev2.RemoteEventEdit || remote.GetTargetMessage() != makeMessageID(testChatID, 201) || remote.GetSender().Sender != makeUserID(2000) {
		t.Fatalf("edit target=%s sender=%s", remote.GetTargetMessage(), remote.GetSender().Sender)
	}
	part := bridgedTextPart(testChatID, 201, 2000, "synthetic original", 0)
	converted, err := remote.ConvertEdit(context.Background(), nil, nil, []*database.Message{part})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.ModifiedParts) != 1 || converted.ModifiedParts[0].Content.Body != "synthetic edited text" || converted.ModifiedParts[0].Content.MsgType != event.MsgText {
		t.Fatalf("edit parts = %+v", converted.ModifiedParts)
	}
	meta := converted.ModifiedParts[0].Part.Metadata.(*KakaoMessageMetadata)
	if meta.Revision != 1 || meta.Preview != "synthetic edited text" {
		t.Fatalf("metadata = %+v", meta)
	}
	// Replay of the same revision after it was applied is not edited again.
	if _, err := remote.ConvertEdit(context.Background(), nil, nil, []*database.Message{part}); !errors.Is(err, bridgev2.ErrIgnoringRemoteEvent) {
		t.Fatalf("replayed edit err = %v", err)
	}
}

func TestInboundEditByAnotherMemberIsIgnored(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	edit := events.MessageEdited{ChatID: testChatID, LogID: 202, AuthorID: 4000, TargetLogID: 201, TargetRevision: 1,
		Modified: events.TextMessage{ChatID: testChatID, LogID: 201, AuthorID: 4000, Message: "synthetic forged edit", Revision: 1}}
	remote := kc.remoteEventFor(edit).(bridgev2.RemoteEdit)
	part := bridgedTextPart(testChatID, 201, 2000, "synthetic original", 0)
	if _, err := remote.ConvertEdit(context.Background(), nil, nil, []*database.Message{part}); !errors.Is(err, bridgev2.ErrIgnoringRemoteEvent) {
		t.Fatalf("edit of another member's message err = %v", err)
	}
}

func TestInboundDeleteRemovesTargetAsTheDeleter(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	deletion := events.MessageDeleted{ChatID: testChatID, LogID: 204, AuthorID: 2000, TargetLogID: 203}
	remote, ok := kc.remoteEventFor(deletion).(bridgev2.RemoteMessageRemove)
	if !ok {
		t.Fatalf("deletion maps to %T", kc.remoteEventFor(deletion))
	}
	if _, isMessage := remote.(bridgev2.RemoteMessage); isMessage {
		t.Fatal("a deletion must not require its own mapping to commit")
	}
	if remote.GetType() != bridgev2.RemoteEventMessageRemove || remote.GetTargetMessage() != makeMessageID(testChatID, 203) || remote.GetSender().Sender != makeUserID(2000) {
		t.Fatalf("remove target=%s sender=%s", remote.GetTargetMessage(), remote.GetSender().Sender)
	}
}

func TestDeletedMessageBecomesPlaceholderWithoutContent(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	deleted := events.DeletedMessage{ChatID: testChatID, LogID: 203, AuthorID: 2000, BaseType: chat.TextType}
	remote, ok := kc.remoteEventFor(deleted).(bridgev2.RemoteMessage)
	if !ok || remote.GetID() != makeMessageID(testChatID, 203) {
		t.Fatalf("deleted message maps to %T", kc.remoteEventFor(deleted))
	}
	converted, err := remote.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := converted.Parts[0].Content
	if content.MsgType != event.MsgNotice || !strings.Contains(content.Body, "deleted") {
		t.Fatalf("placeholder = %+v", content)
	}
}

func TestEditedTextRecordsItsRevision(t *testing.T) {
	converted, err := convertText(context.Background(), nil, nil, events.TextMessage{ChatID: testChatID, LogID: 201, AuthorID: 2000, Message: "synthetic edited text", Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if meta := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata); meta.Revision != 1 {
		t.Fatalf("revision = %d", meta.Revision)
	}
}

func TestEditAndDeleteFeedsCommitWhenTargetIsNotBridged(t *testing.T) {
	for _, evt := range []events.Event{
		events.MessageEdited{ChatID: testChatID, LogID: 202, AuthorID: 2000, TargetLogID: 201, TargetRevision: 1,
			Modified: events.TextMessage{ChatID: testChatID, LogID: 201, AuthorID: 2000, Message: "synthetic edited text", Revision: 1}},
		events.MessageDeleted{ChatID: testChatID, LogID: 204, AuthorID: 2000, TargetLogID: 203},
	} {
		kc, _ := newTestClient(t, nil)
		fake := &fakeKakao{}
		kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult { return bridgev2.EventHandlingResultIgnored }
		if !kc.handleEvent(fake, evt) || len(fake.committed()) != 1 {
			t.Fatalf("%T with a missing target was not committed", evt)
		}
	}
}

type fetchingKakao struct {
	*fakeKakao
	results  [][]events.Event
	requests [][]int64
	fetchErr error
}

func (f *fetchingKakao) GetMessages(_ context.Context, chatID int64, logIDs []int64) ([]events.Event, error) {
	f.requests = append(f.requests, append([]int64{chatID}, logIDs...))
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if len(f.results) == 0 {
		return nil, nil
	}
	got := f.results[0]
	f.results = f.results[1:]
	return got, nil
}

// Regression (owned acceptance, 2026-10-10): an edit made while the bridge
// was offline arrives in catch-up without content. Reading it with a SYNCMSG
// range returned nothing and the edit was lost. Like the Mac client, the
// edited message is now read by position with GETMSGS before the edit is
// queued; a failed read leaves the feed uncommitted and queues nothing.
func TestCatchUpEditFetchesTheEditedMessageBeforeQueueing(t *testing.T) {
	feed := events.MessageEdited{ChatID: testChatID, LogID: 202, AuthorID: 2000, TargetLogID: 201, TargetRevision: 2}
	kc, _ := newTestClient(t, nil)
	source := &fetchingKakao{fakeKakao: &fakeKakao{}, fetchErr: errors.New("synthetic read failure")}
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResultSuccess
	}
	if kc.handleEvent(source, feed) || queued != 0 || len(source.committed()) != 0 {
		t.Fatal("edit with an unreadable target was queued or committed")
	}
	source.fetchErr = nil
	source.results = [][]events.Event{{events.TextMessage{ChatID: testChatID, LogID: 201, AuthorID: 2000, Message: "synthetic second edit", Revision: 2}}}
	var got *kakaoEdit
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		got, _ = remote.(*kakaoEdit)
		return bridgev2.EventHandlingResultSuccess
	}
	if !kc.handleEvent(source, feed) || got == nil || len(source.committed()) != 1 {
		t.Fatal("completed edit was not queued and committed")
	}
	if last := source.requests[len(source.requests)-1]; len(last) != 2 || last[0] != testChatID || last[1] != 201 {
		t.Fatalf("GETMSGS request = %v", last)
	}
	if text, ok := editedText(got.edit.Modified, 201); !ok || text != "synthetic second edit" {
		t.Fatalf("modified = %#v", got.edit.Modified)
	}
}

// The Mac client marks a deleted message in place. A delete feed whose target
// was already bridged as the deleted placeholder (catch-up delivers both) is
// committed without redacting the placeholder.
func TestDeleteFeedForAnAlreadyDeletedPlaceholderIsCommittedWithoutRedaction(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	placeholder := newKakaoMessageMetadata(testChatID, 203, 2000, chat.TextType|messagetype.DeletedAllChatTypeFlag, "[deleted]", 0)
	if err := kc.login.Bridge.DB.Message.Insert(ctx, &database.Message{
		BridgeID: kc.login.Bridge.ID, ID: makeMessageID(testChatID, 203), MXID: "$placeholder",
		Room: makePortalKey(testChatID, kc.login.ID), SenderID: makeUserID(2000), SenderMXID: "@kakao_2000:test",
		Timestamp: time.Unix(1700000000, 0), Metadata: placeholder,
	}); err != nil {
		t.Fatal(err)
	}
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult { queued++; return bridgev2.EventHandlingResultSuccess }
	if !kc.handleEvent(backend, events.MessageDeleted{ChatID: testChatID, LogID: 204, AuthorID: 2000, TargetLogID: 203}) {
		t.Fatal("delete feed for a placeholder was not handled")
	}
	if queued != 0 || len(backend.committed()) != 1 {
		t.Fatalf("queued=%d commits=%d", queued, len(backend.committed()))
	}
}
