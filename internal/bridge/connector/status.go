package connector

import (
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/client"
)

// noticeStatus is a failed Matrix event status that is also posted to the
// room as a notice. Certainty tells Matrix whether KakaoTalk may still have
// received the event.
func noticeStatus(err error, status event.MessageStatus, reason event.MessageStatusReason, certain bool, message string) bridgev2.MessageStatus {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(status).
		WithErrorReason(reason).
		WithIsCertain(certain).
		WithMessage(message).
		WithSendNotice(true)
}

// certainRefusal reports a Matrix event the bridge refused before anything
// reached KakaoTalk, because KakaoTalk cannot represent it.
func certainRefusal(err error, message string) bridgev2.MessageStatus {
	return noticeStatus(err, event.MessageStatusFail, event.MessageStatusUnsupported, true, message)
}

// certainSendFailure reports a source request KakaoTalk certainly did not
// apply. It is not retried.
func certainSendFailure(err error, message string) bridgev2.MessageStatus {
	return noticeStatus(err, event.MessageStatusFail, event.MessageStatusNetworkError, true, message)
}

// uncertainFailure reports a source request that may have been applied. It
// must not be shown as retriable, because a resend could duplicate it.
func uncertainFailure(err error, message string) bridgev2.MessageStatus {
	return noticeStatus(err, event.MessageStatusFail, event.MessageStatusNetworkError, false, message)
}

// retriableFetchFailure reports Matrix media the bridge could not fetch.
// Nothing reached KakaoTalk, so the user may safely send it again.
func retriableFetchFailure(err error, message string) bridgev2.MessageStatus {
	return noticeStatus(err, event.MessageStatusRetriable, event.MessageStatusNetworkError, true, message)
}

// replyTargetStatus refuses a Matrix reply whose target cannot be mapped to
// exactly one KakaoTalk message in this chat. Nothing was sent, so the
// refusal is certain; sending without the relation is left to the user.
func replyTargetStatus(err error) error {
	return certainRefusal(err, "The message you replied to is not a bridged KakaoTalk message in this chat, so the reply was not sent. Send it without the reply to deliver it.")
}

// outboundSendError classifies a failed single Kakao send for Matrix. A
// server status reply is a certain refusal. Any other failure may have been
// delivered; it is never retried, and it must not be shown as retriable,
// because a manual resend could duplicate a delivered message.
func outboundSendError(err error) error {
	var statusErr client.StatusError
	if errors.As(err, &statusErr) {
		return certainSendFailure(err, "KakaoTalk refused this message.")
	}
	return uncertainFailure(err, "KakaoTalk did not confirm this message, and it may have been delivered. Check KakaoTalk before sending it again.")
}

func outboundAcceptedWithoutPosition(err error) error {
	return uncertainFailure(err, "KakaoTalk accepted this message without identifying it, so it is probably delivered but cannot be linked. Check KakaoTalk before sending it again.")
}
