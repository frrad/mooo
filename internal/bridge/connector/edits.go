package connector

import (
	"context"
	"fmt"
	"time"

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
// edits of messages without text, is ignored rather than repeated. A message
// bridged as several parts (a sticker with text, or a reply carrying a
// sticker) has only its text part edited in Matrix; the other parts keep
// their event and take the new revision and reply preview.
func (e *kakaoEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	text, textPartID, ok := editedText(e.edit.Modified, e.edit.TargetLogID)
	if !ok {
		return nil, fmt.Errorf("%w: edited content is not text", bridgev2.ErrIgnoringRemoteEvent)
	}
	var target *database.Message
	for _, part := range existing {
		if part != nil && part.PartID == textPartID {
			target = part
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: edit target not bridged", bridgev2.ErrIgnoringRemoteEvent)
	}
	stored, ok := target.Metadata.(*KakaoMessageMetadata)
	if !ok || stored == nil {
		return nil, fmt.Errorf("%w: edit target has no Kakao metadata", bridgev2.ErrIgnoringRemoteEvent)
	}
	if stored.AuthorID != 0 && e.edit.AuthorID != 0 && stored.AuthorID != e.edit.AuthorID {
		return nil, fmt.Errorf("%w: edit is not from the message author", bridgev2.ErrIgnoringRemoteEvent)
	}
	if stored.Revision >= e.edit.TargetRevision {
		return nil, fmt.Errorf("%w: edit revision already applied", bridgev2.ErrIgnoringRemoteEvent)
	}
	updated := *stored
	updated.Revision = e.edit.TargetRevision
	updated.Preview = text
	target.Metadata = &updated
	converted := &bridgev2.ConvertedEdit{ModifiedParts: []*bridgev2.ConvertedEditPart{{
		Part:    target,
		Type:    event.EventMessage,
		Content: &event.MessageEventContent{MsgType: event.MsgText, Body: text},
	}}}
	for _, part := range existing {
		if part == nil || part == target {
			continue
		}
		meta, ok := part.Metadata.(*KakaoMessageMetadata)
		if !ok || meta == nil {
			continue
		}
		companion := *meta
		companion.Revision = e.edit.TargetRevision
		companion.Preview = truncateReplyPreview(text)
		part.Metadata = &companion
		converted.ModifiedParts = append(converted.ModifiedParts, &bridgev2.ConvertedEditPart{
			Part: part, Type: event.EventMessage, Content: &event.MessageEventContent{}, DontBridge: true,
		})
	}
	return converted, nil
}

// editedText returns the edited text and the part that shows it. A sticker
// edit carries the whole type-12/20 log with its new text (observed); its
// text is the sticker's text part.
func editedText(modified events.Event, target int64) (string, networkid.PartID, bool) {
	switch m := modified.(type) {
	case events.TextMessage:
		return m.Message, "", m.LogID == target
	case events.ReplyMessage:
		return m.Message, "", m.LogID == target
	case events.StickerMessage:
		return m.Message, stickerTextPartID, m.LogID == target && m.Message != ""
	default:
		return "", "", false
	}
}

// completeEdit reads the edited message for an edit feed delivered without it
// (catch-up), by position with GETMSGS like the Mac client. A failed read
// leaves the feed uncommitted; a target the server does not return leaves
// nothing to apply.
func (kc *KakaoClient) completeEdit(ctx context.Context, c historyAPI, edit events.MessageEdited) (events.MessageEdited, error) {
	if edit.Modified != nil {
		return edit, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	got, err := c.GetMessages(ctx, edit.ChatID, []int64{edit.TargetLogID})
	if err != nil {
		return edit, err
	}
	for _, evt := range got {
		if chatID, logID, ok := events.MessagePosition(evt); ok && chatID == edit.ChatID && logID == edit.TargetLogID {
			edit.Modified = evt
		}
	}
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

// targetAlreadyDeleted reports whether a delete feed's target was bridged as
// the deleted placeholder; the Mac client marks deletions in place, so there
// is nothing more to remove.
func (kc *KakaoClient) targetAlreadyDeleted(deletion events.MessageDeleted) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	part, err := kc.store.firstMessagePart(ctx, kc.login.ID, makeMessageID(deletion.ChatID, deletion.TargetLogID))
	if err != nil || part == nil {
		return false
	}
	meta, ok := part.Metadata.(*KakaoMessageMetadata)
	return ok && meta != nil && meta.Type&messagetype.DeletedAllChatTypeFlag != 0
}
