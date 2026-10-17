package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

var (
	_ bridgev2.EditHandlingNetworkAPI      = (*KakaoClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI = (*KakaoClient)(nil)

	errEditNotAllowed   = errors.New("connector: KakaoTalk does not allow this edit")
	errDeleteNotAllowed = errors.New("connector: KakaoTalk does not allow this deletion")
)

// The Mac client allows editing for 24 hours (hard-coded) and deleting for
// everyone for messageDeleteTimeV2 minutes, 1440 by default. mooo has no
// server setting and uses the default.
const (
	kakaoEditWindow   = 24 * time.Hour
	kakaoDeleteWindow = 1440 * time.Minute
)

// HandleMatrixEdit sends one KakaoTalk edit of the user's own text, reply,
// or text sent with a sticker. Kakao's own rules are checked first; a
// refusal is certain, a transport failure is ambiguous, and nothing is
// retried.
func (kc *KakaoClient) HandleMatrixEdit(ctx context.Context, msg *bridgev2.MatrixEdit) error {
	if msg == nil || msg.Portal == nil || msg.Content == nil || msg.EditTarget == nil {
		return errSourceAccessRemoved
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return err
	}
	if err = kc.checkSourceAccess(ctx, chatID); err != nil {
		return err
	}
	meta, _ := msg.EditTarget.Metadata.(*KakaoMessageMetadata)
	switch {
	case meta == nil || meta.ChatID != chatID || meta.LogID <= 0:
		return certainRefusal(errEditNotAllowed, "This message cannot be edited in KakaoTalk.")
	case meta.AuthorID != kc.userID:
		return certainRefusal(errEditNotAllowed, "KakaoTalk only allows editing your own messages; the edit was not sent.")
	case meta.Type&messagetype.DeletedAllChatTypeFlag != 0:
		return certainRefusal(errEditNotAllowed, "This KakaoTalk message was deleted and cannot be edited.")
	case meta.Type != messagetype.Text && !editablePart(meta.Type, msg.EditTarget.PartID):
		return certainRefusal(errEditNotAllowed, "The bridge can only edit the text of KakaoTalk messages, replies and stickers; the edit was not sent.")
	case time.Since(msg.EditTarget.Timestamp) > kakaoEditWindow:
		return certainRefusal(errEditNotAllowed, "KakaoTalk only allows editing messages for 24 hours; the edit was not sent.")
	}
	body := msg.Content.Body
	if body == meta.Preview {
		// Like the Mac client, an unchanged text is not sent.
		return nil
	}
	if body == "" || len(body) > maxTextLength {
		return certainRefusal(errEditNotAllowed, "The edited text is empty or too long for KakaoTalk; the edit was not sent.")
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return bridgev2.ErrNotLoggedIn
	}
	extra := "{}"
	if meta.Type != messagetype.Text {
		if extra, err = kc.editExtra(ctx, c, chatID, meta); err != nil {
			return err
		}
	}
	if err := kc.beginOutboundEvent(ctx, msg.Event); err != nil {
		return err
	}
	revision, err := c.ModifyMessage(ctx, chat.ModifyRequest{ChatID: chatID, LogID: meta.LogID, Type: meta.Type, Message: body, Extra: extra})
	if err != nil {
		return outboundSendError(err)
	}
	updated := *meta
	updated.Revision = revision
	updated.Preview = body
	msg.EditTarget.Metadata = &updated
	kc.recordEditOnOtherParts(ctx, msg.EditTarget, revision, body)
	return nil
}

// editablePart reports whether a bridged part shows the text of a reply or
// of a sticker sent with text, which the Mac client lets its author edit.
// The sticker parts themselves are not editable.
func editablePart(messageType int32, partID networkid.PartID) bool {
	switch messageType {
	case messagetype.Sticker, messagetype.AnimatedSticker:
		return partID == stickerTextPartID
	case messagetype.Reply:
		return partID == ""
	default:
		return false
	}
}

// editExtra builds the MODIFYMSG extra for a sticker or reply edit from the
// message as KakaoTalk has it now, like the Mac client builds it from its
// stored copy: the original sticker or reply keys are carried over so the
// edit keeps them. Nothing has been sent yet, so every failure is certain.
func (kc *KakaoClient) editExtra(ctx context.Context, c kakaoClient, chatID int64, meta *KakaoMessageMetadata) (string, error) {
	source, err := c.EditSource(ctx, chatID, meta.LogID)
	switch {
	case errors.Is(err, chat.ErrEditSourceMissing):
		return "", certainRefusal(err, "KakaoTalk did not return this message, so the edit was not sent.")
	case err != nil:
		return "", certainSendFailure(err, "The bridge could not read this message from KakaoTalk; the edit was not sent.")
	case source.AuthorID != kc.userID || source.Type != meta.Type:
		return "", certainRefusal(errEditNotAllowed, "This KakaoTalk message cannot be edited; the edit was not sent.")
	}
	extra, err := chat.ModifyExtra(source)
	if err != nil {
		return "", certainRefusal(errEditNotAllowed, "KakaoTalk does not allow editing this message (a sticker without text or a sticker-only reply); the edit was not sent.")
	}
	return extra, nil
}

// recordEditOnOtherParts gives a sent edit's revision and text to the
// message's other bridged parts, such as the sticker beside an edited text,
// so a later reply to them quotes the new text.
func (kc *KakaoClient) recordEditOnOtherParts(ctx context.Context, target *database.Message, revision int64, text string) {
	parts, err := kc.store.messageParts(ctx, kc.login.ID, target.ID)
	if err != nil {
		kc.log().Warn().Err(err).Msg("Failed to read the edited message's other parts")
		return
	}
	for _, part := range parts {
		meta, ok := part.Metadata.(*KakaoMessageMetadata)
		if part.PartID == target.PartID || !ok || meta == nil {
			continue
		}
		updated := *meta
		updated.Revision = revision
		updated.Preview = truncateReplyPreview(text)
		part.Metadata = &updated
		if err := kc.store.updateMessage(ctx, part); err != nil {
			kc.log().Warn().Err(err).Msg("Failed to record an edit on a message part")
		}
	}
}

// HandleMatrixMessageRemove deletes the user's own message for everyone in
// KakaoTalk once. KakaoTalk has no member-wide delete of others' messages in
// regular groups.
func (kc *KakaoClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if msg == nil || msg.Portal == nil || msg.TargetMessage == nil {
		return errSourceAccessRemoved
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return err
	}
	if err = kc.checkSourceAccess(ctx, chatID); err != nil {
		return err
	}
	meta, _ := msg.TargetMessage.Metadata.(*KakaoMessageMetadata)
	switch {
	case meta == nil || meta.ChatID != chatID || meta.LogID <= 0:
		return certainRefusal(errDeleteNotAllowed, "This message cannot be deleted in KakaoTalk.")
	case meta.Type&messagetype.DeletedAllChatTypeFlag != 0:
		// Already deleted in KakaoTalk; removing the placeholder is local.
		return nil
	case meta.Type == messagetype.Mvoip:
		return certainRefusal(errCallDeleteNotSupported, "Deleting KakaoTalk call records is not supported; the message was removed only from Matrix.")
	case meta.AuthorID != kc.userID:
		return certainRefusal(errDeleteNotAllowed, "KakaoTalk only allows deleting your own messages for everyone; the message was removed only from Matrix.")
	case meta.Type == messagetype.Leverage:
		return certainRefusal(errLeverageDeleteNotSupported, "Deleting shared KakaoTalk cards is not supported; the message was removed only from Matrix.")
	case time.Since(msg.TargetMessage.Timestamp) > kakaoDeleteWindow:
		return certainRefusal(errDeleteNotAllowed, "KakaoTalk only allows deleting messages for everyone for 24 hours; the message was removed only from Matrix.")
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return bridgev2.ErrNotLoggedIn
	}
	if err := kc.beginOutboundEvent(ctx, msg.Event); err != nil {
		return err
	}
	err = c.DeleteMessage(ctx, chat.DeleteRequest{ChatID: chatID, LogID: meta.LogID})
	var status client.StatusError
	switch {
	case err == nil, errors.As(err, &status) && status.Status == chat.StatusAlreadyDeleted:
		kc.recordMatrixDeletion(ctx, msg.TargetMessage.ID)
		return nil
	case errors.As(err, &status) && status.Status == chat.StatusDeleteTimeExpired:
		return certainRefusal(errDeleteNotAllowed, "KakaoTalk refused: the time limit for deleting this message for everyone has passed.")
	case errors.As(err, &status) && status.Status == chat.StatusDeleteTypeNotAllowed:
		return certainRefusal(errDeleteNotAllowed, "KakaoTalk refused: this message type cannot be deleted for everyone.")
	default:
		return outboundSendError(err)
	}
}

// The redaction removes the message mapping. Outbound sends do not move the
// delivery cursor, so catch-up can later deliver the deleted-flagged log; a
// durable tombstone keeps it from reappearing as a placeholder.
func matrixDeletionKey(kc *KakaoClient, messageID networkid.MessageID) string {
	return fmt.Sprintf("kakao:matrix-deleted:%s:%s", kc.login.ID, messageID)
}

func (kc *KakaoClient) recordMatrixDeletion(ctx context.Context, messageID networkid.MessageID) {
	if _, err := kc.store.putIfAbsent(ctx, matrixDeletionKey(kc, messageID), kvTimeStamp(time.Now())); err != nil {
		kc.log().Warn().Err(err).Msg("Failed to record a Matrix deletion tombstone")
	}
}

func (kc *KakaoClient) deletedFromMatrix(messageID networkid.MessageID) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, found, err := kc.store.get(ctx, matrixDeletionKey(kc, messageID))
	return err == nil && found
}
