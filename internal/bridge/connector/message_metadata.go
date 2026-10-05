package connector

import (
	"errors"
	"fmt"
	"unicode/utf16"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/frrad/mooo/internal/protocol/chat"
)

var (
	errMissingReplyMetadata = errors.New("connector: reply target has no Kakao message metadata")
	errInvalidReplyTarget   = errors.New("connector: reply target metadata is invalid")
	errCrossChatReply       = errors.New("connector: reply target belongs to another chat")
	errReplySenderMismatch  = errors.New("connector: reply target sender does not match metadata")
)

// KakaoMessageMetadata is the source identity required to construct a type-26
// reply. It is persisted on every bridged message part so replies remain
// deterministic after a restart.
type KakaoMessageMetadata struct {
	ChatID        int64  `json:"chat_id"`
	LogID         int64  `json:"log_id"`
	AuthorID      int64  `json:"author_id"`
	Type          int32  `json:"type"`
	Preview       string `json:"preview"`
	LinkID        int64  `json:"link_id,omitempty"`
	ConversionGap string `json:"conversion_gap,omitempty"`
}

func (m KakaoMessageMetadata) String() string {
	return fmt.Sprintf("KakaoMessageMetadata{chatId=%d, logId=%d, authorId=%d, type=%d, preview=<redacted>, linkId=%d}", m.ChatID, m.LogID, m.AuthorID, m.Type, m.LinkID)
}

func (m KakaoMessageMetadata) GoString() string { return m.String() }

var _ database.MetaMerger = (*KakaoMessageMetadata)(nil)

func (m *KakaoMessageMetadata) CopyFrom(other any) {
	if source, ok := other.(*KakaoMessageMetadata); ok && source != nil {
		*m = *source
	}
}

func newKakaoMessageMetadata(chatID, logID, authorID int64, messageType int32, preview string, linkID int64) *KakaoMessageMetadata {
	return &KakaoMessageMetadata{
		ChatID: chatID, LogID: logID, AuthorID: authorID, Type: messageType,
		Preview: truncateReplyPreview(preview), LinkID: linkID,
	}
}

func (m *KakaoMessageMetadata) valid() bool {
	return m != nil && m.ChatID > 0 && m.LogID > 0 && m.AuthorID > 0 && m.Type > 0 && m.LinkID >= 0
}

func metadataFromMessage(message *database.Message) (*KakaoMessageMetadata, error) {
	if message == nil || message.Metadata == nil {
		return nil, errMissingReplyMetadata
	}
	var metadata *KakaoMessageMetadata
	switch value := message.Metadata.(type) {
	case *KakaoMessageMetadata:
		metadata = value
	case KakaoMessageMetadata:
		metadata = &value
	default:
		return nil, errMissingReplyMetadata
	}
	if !metadata.valid() {
		return nil, errInvalidReplyTarget
	}
	if message.SenderID == "" || message.SenderID != makeUserID(metadata.AuthorID) {
		return nil, errReplySenderMismatch
	}
	return metadata, nil
}

func replyTargetFor(message *database.Message, portal networkid.PortalKey) (chat.ReplyTarget, error) {
	if message == nil || message.Room.ID != "" && message.Room.ID != portal.ID || message != nil && message.Room.Receiver != "" && message.Room.Receiver != portal.Receiver {
		return chat.ReplyTarget{}, errCrossChatReply
	}
	metadata, err := metadataFromMessage(message)
	if err != nil {
		return chat.ReplyTarget{}, err
	}
	chatID, logID, err := parseMessageID(message.ID)
	if err != nil || chatID != metadata.ChatID || chatID != mustParsePortalID(portal.ID) || logID != metadata.LogID {
		return chat.ReplyTarget{}, errCrossChatReply
	}
	return chat.ReplyTarget{
		LogID: metadata.LogID, UserID: metadata.AuthorID, LinkID: metadata.LinkID,
		Type: metadata.Type, Message: metadata.Preview,
	}, nil
}

func mustParsePortalID(value networkid.PortalID) int64 {
	parsed, _ := parseChatID(value)
	return parsed
}

func truncateReplyPreview(message string) string {
	units := utf16.Encode([]rune(message))
	if len(units) <= 100 {
		return message
	}
	units = units[:100]
	if last := units[len(units)-1]; last >= 0xd800 && last <= 0xdbff {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}
