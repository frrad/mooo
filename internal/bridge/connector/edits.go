package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

// kakaoEdit applies a KakaoTalk edit feed to its bridged target. It is not a
// RemoteMessage: the feed's own position commits even when the target was
// never bridged.
type kakaoEdit struct {
	simplevent.EventMeta
	edit events.MessageEdited
}

var _ bridgev2.RemoteEdit = (*kakaoEdit)(nil)

func (kc *KakaoClient) editEvent(edit events.MessageEdited) *kakaoEdit {
	meta := kc.messageMeta(edit.ChatID, edit.LogID, edit.AuthorID, edit.SentAt)
	meta.Type = bridgev2.RemoteEventEdit
	meta.CreatePortal = false
	return &kakaoEdit{EventMeta: meta, edit: edit}
}

func (e *kakaoEdit) GetTargetMessage() networkid.MessageID {
	return makeMessageID(e.edit.ChatID, e.edit.TargetLogID)
}

// ConvertEdit replaces the bridged text when the edit is newer than the
// stored revision and comes from the message's author. Anything else, and
// edits of non-text messages, is ignored rather than repeated.
func (e *kakaoEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	if len(existing) == 0 || existing[0] == nil {
		return nil, fmt.Errorf("%w: edit target not bridged", bridgev2.ErrIgnoringRemoteEvent)
	}
	part := existing[0]
	stored, ok := part.Metadata.(*KakaoMessageMetadata)
	if !ok || stored == nil {
		return nil, fmt.Errorf("%w: edit target has no Kakao metadata", bridgev2.ErrIgnoringRemoteEvent)
	}
	if stored.AuthorID != 0 && e.edit.AuthorID != 0 && stored.AuthorID != e.edit.AuthorID {
		return nil, fmt.Errorf("%w: edit is not from the message author", bridgev2.ErrIgnoringRemoteEvent)
	}
	if stored.Revision >= e.edit.TargetRevision {
		return nil, fmt.Errorf("%w: edit revision already applied", bridgev2.ErrIgnoringRemoteEvent)
	}
	text, ok := editedText(e.edit.Modified, e.edit.TargetLogID)
	if !ok {
		return nil, fmt.Errorf("%w: edited content is not text", bridgev2.ErrIgnoringRemoteEvent)
	}
	updated := *stored
	updated.Revision = e.edit.TargetRevision
	updated.Preview = text
	part.Metadata = &updated
	return &bridgev2.ConvertedEdit{ModifiedParts: []*bridgev2.ConvertedEditPart{{
		Part:    part,
		Type:    event.EventMessage,
		Content: &event.MessageEventContent{MsgType: event.MsgText, Body: text},
	}}}, nil
}

func editedText(modified events.Event, target int64) (string, bool) {
	switch m := modified.(type) {
	case events.TextMessage:
		return m.Message, m.LogID == target
	case events.ReplyMessage:
		return m.Message, m.LogID == target
	default:
		return "", false
	}
}

// completeEdit fetches the edited message for an edit feed delivered without
// it (catch-up). The fetch is a read; failure leaves the feed uncommitted.
func (kc *KakaoClient) completeEdit(ctx context.Context, c kakaoClient, edit events.MessageEdited) (events.MessageEdited, error) {
	if edit.Modified != nil {
		return edit, nil
	}
	source, ok := c.(groupHistorySource)
	if !ok {
		return edit, fmt.Errorf("connector: edited message cannot be fetched")
	}
	page, err := source.ReadHistoryPage(ctx, edit.ChatID, edit.TargetLogID-1, edit.TargetLogID, 1)
	if err != nil {
		return edit, err
	}
	for _, evt := range page.Events {
		if chatID, logID, ok := events.MessagePosition(evt); ok && chatID == edit.ChatID && logID == edit.TargetLogID {
			edit.Modified = evt
			return edit, nil
		}
	}
	// The target is gone (for example deleted); there is nothing to apply.
	edit.Modified = events.DeletedMessage{ChatID: edit.ChatID, LogID: edit.TargetLogID}
	return edit, nil
}

func (kc *KakaoClient) deletionEvent(deletion events.MessageDeleted) *simplevent.MessageRemove {
	meta := kc.messageMeta(deletion.ChatID, deletion.LogID, deletion.AuthorID, deletion.SentAt)
	meta.Type = bridgev2.RemoteEventMessageRemove
	meta.CreatePortal = false
	return &simplevent.MessageRemove{EventMeta: meta, TargetMessage: makeMessageID(deletion.ChatID, deletion.TargetLogID)}
}

func (kc *KakaoClient) deletedMessageEvent(deleted events.DeletedMessage) bridgev2.RemoteEvent {
	metadata := newKakaoMessageMetadata(deleted.ChatID, deleted.LogID, deleted.AuthorID, deleted.BaseType|messagetype.DeletedAllChatTypeFlag, "[deleted]", 0)
	return newMessage(kc.messageMeta(deleted.ChatID, deleted.LogID, deleted.AuthorID, deleted.SentAt), makeMessageID(deleted.ChatID, deleted.LogID), noticeData{
		Body: "This KakaoTalk message was deleted.", Metadata: metadata,
	}, convertNoticeWithMetadata)
}
