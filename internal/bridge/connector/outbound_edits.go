package connector

import (
	"context"
	"errors"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

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

// HandleMatrixEdit sends one KakaoTalk edit of the user's own text message.
// Kakao's own rules are checked first; a refusal is certain, a transport
// failure is ambiguous, and nothing is retried.
func (kc *KakaoClient) HandleMatrixEdit(ctx context.Context, msg *bridgev2.MatrixEdit) error {
	if msg == nil || msg.Portal == nil || msg.Content == nil || msg.EditTarget == nil {
		return errSourceAccessRemoved
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return err
	}
	if err = kc.checkSourceAccess(ctx, chatID, msg.Portal); err != nil {
		return err
	}
	meta, _ := msg.EditTarget.Metadata.(*KakaoMessageMetadata)
	switch {
	case meta == nil || meta.ChatID != chatID || meta.LogID <= 0:
		return editRejected("This message cannot be edited in KakaoTalk.")
	case meta.AuthorID != kc.userID:
		return editRejected("KakaoTalk only allows editing your own messages; the edit was not sent.")
	case meta.Type&messagetype.DeletedAllChatTypeFlag != 0:
		return editRejected("This KakaoTalk message was deleted and cannot be edited.")
	case meta.Type != chat.TextType:
		return editRejected("The bridge can only edit KakaoTalk text messages; the edit was not sent.")
	case time.Since(msg.EditTarget.Timestamp) > kakaoEditWindow:
		return editRejected("KakaoTalk only allows editing messages for 24 hours; the edit was not sent.")
	}
	body := msg.Content.Body
	if body == meta.Preview {
		// Like the Mac client, an unchanged text is not sent.
		return nil
	}
	if body == "" || len(body) > maxTextLength {
		return editRejected("The edited text is empty or too long for KakaoTalk; the edit was not sent.")
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
	revision, err := c.ModifyMessage(ctx, chat.ModifyRequest{ChatID: chatID, LogID: meta.LogID, Type: meta.Type, Message: body, Extra: "{}"})
	if err != nil {
		return outboundSendError(err)
	}
	updated := *meta
	updated.Revision = revision
	updated.Preview = body
	msg.EditTarget.Metadata = &updated
	return nil
}

func editRejected(message string) error {
	return bridgev2.WrapErrorInStatus(errEditNotAllowed).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithMessage(message).
		WithSendNotice(true)
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
	if err = kc.checkSourceAccess(ctx, chatID, msg.Portal); err != nil {
		return err
	}
	meta, _ := msg.TargetMessage.Metadata.(*KakaoMessageMetadata)
	switch {
	case meta == nil || meta.ChatID != chatID || meta.LogID <= 0:
		return deleteRejected("This message cannot be deleted in KakaoTalk.")
	case meta.Type&messagetype.DeletedAllChatTypeFlag != 0:
		// Already deleted in KakaoTalk; removing the placeholder is local.
		return nil
	case meta.AuthorID != kc.userID:
		return deleteRejected("KakaoTalk only allows deleting your own messages for everyone; the message was removed only from Matrix.")
	case time.Since(msg.TargetMessage.Timestamp) > kakaoDeleteWindow:
		return deleteRejected("KakaoTalk only allows deleting messages for everyone for 24 hours; the message was removed only from Matrix.")
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
	case err == nil:
		return nil
	case errors.As(err, &status) && status.Status == chat.StatusAlreadyDeleted:
		return nil
	case errors.As(err, &status) && status.Status == chat.StatusDeleteTimeExpired:
		return deleteRejected("KakaoTalk refused: the time limit for deleting this message for everyone has passed.")
	case errors.As(err, &status) && status.Status == chat.StatusDeleteTypeNotAllowed:
		return deleteRejected("KakaoTalk refused: this message type cannot be deleted for everyone.")
	default:
		return outboundSendError(err)
	}
}

func deleteRejected(message string) error {
	return bridgev2.WrapErrorInStatus(errDeleteNotAllowed).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithMessage(message).
		WithSendNotice(true)
}
