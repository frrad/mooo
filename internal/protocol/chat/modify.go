package chat

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/messagetype"
)

const (
	ModifyCommand = "MODIFYMSG"
	DeleteCommand = "DELETEMSG"

	// Server statuses the Mac client special-cases for DELETEMSG.
	StatusDeleteTypeNotAllowed = -210
	StatusAlreadyDeleted       = -211
	StatusDeleteTimeExpired    = -212
)

// ModifyRequest edits one own message. Type is the message's original type;
// Extra replaces its attachment.
type ModifyRequest struct {
	ChatID  int64
	LogID   int64
	Type    int32
	Message string
	Extra   string
}

func (r ModifyRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidChatID
	}
	if r.LogID <= 0 {
		return nil, ErrInvalidMessageID
	}
	if r.Type <= 0 || r.Message == "" || !utf8.ValidString(r.Message) || len(r.Message) > maxTextBytes || strings.IndexByte(r.Message, 0) >= 0 || !json.Valid([]byte(r.Extra)) {
		return nil, ErrInvalidMessage
	}
	return bson.Marshal(bson.D{
		{Key: "chatId", Value: r.ChatID}, {Key: "logId", Value: r.LogID}, {Key: "type", Value: r.Type},
		{Key: "msg", Value: r.Message}, {Key: "extra", Value: r.Extra},
	})
}

// DecodeModifyResponse returns the edited message's new revision. The
// response carries the edit feed and the edited message, like SYNCMODMSG.
func DecodeModifyResponse(body []byte, logID int64) (int64, error) {
	value, err := bson.Raw(body).LookupErr("modifiedChatLog")
	if err != nil || value.Type != bson.TypeEmbeddedDocument {
		return 0, ErrInvalidWriteResp
	}
	modified := value.Document()
	got, ok, err := fields.Int64(modified, "logId")
	if err != nil || !ok || got != logID {
		return 0, ErrInvalidWriteResp
	}
	revision, ok, err := fields.Int64(modified, "revision")
	if err != nil || !ok || revision <= 0 {
		return 0, ErrInvalidWriteResp
	}
	return revision, nil
}

var (
	// ErrNotEditable reports a message the Mac client does not let its
	// author edit: media, a sticker without text, an attachment-only reply.
	ErrNotEditable = errors.New("chat: message is not editable")
	// ErrEditSourceMissing reports that GETMSGS did not return the target.
	ErrEditSourceMissing = errors.New("chat: edit target not returned")
)

// EditSource is the current chat log an edit is built on.
type EditSource struct {
	LogID, AuthorID int64
	Type            int32
	Message         string
	Attachment      string
}

// DecodeEditSource returns logID's chat log from a GETMSGS reply.
func DecodeEditSource(body []byte, logID int64) (EditSource, error) {
	logs, err := DecodeGetMessagesResponse(body)
	if err != nil {
		return EditSource{}, err
	}
	for _, log := range logs {
		got, ok, err := fields.Int64(log, "logId")
		if err != nil || !ok || got != logID {
			continue
		}
		source := EditSource{LogID: got}
		if source.AuthorID, _, err = fields.Int64(log, "authorId"); err != nil {
			return EditSource{}, ErrInvalidGetMessagesResp
		}
		if source.Type, ok, err = fields.Int32(log, "type"); err != nil || !ok {
			return EditSource{}, ErrInvalidGetMessagesResp
		}
		if source.Message, _, err = fields.String(log, "message"); err != nil {
			return EditSource{}, ErrInvalidGetMessagesResp
		}
		if source.Attachment, _, err = fields.String(log, "attachment"); err != nil {
			return EditSource{}, ErrInvalidGetMessagesResp
		}
		return source, nil
	}
	return EditSource{}, ErrEditSourceMissing
}

// Attachment keys the Mac client carries from the original into MODIFYMSG
// extra, so an edit of the text keeps the sticker or the reply.
var (
	emoticonEditKeys = []string{"url", "name", "sound", "path", "path2", "scon", "alt", "xconVersion", "width", "height"}
	replyEditKeys    = []string{"src_logId", "src_userId", "src_linkId", "src_type", "attach_only", "attach_type", "attach_content", "src_message", "src_mentions"}
)

// ModifyExtra builds the MODIFYMSG extra for a text edit of source, as the
// Mac client does with no mentions, spoilers or links in the new text: an
// empty object for a text, and for a sticker with text or a reply the listed
// keys present in the original attachment. Other messages, a sticker without
// text and an attachment-only reply are not editable.
func ModifyExtra(source EditSource) (string, error) {
	var keys []string
	switch source.Type {
	case messagetype.Text:
		return "{}", nil
	case messagetype.AnimatedEmoticon, messagetype.Sticker, messagetype.AnimatedSticker, messagetype.Spritecon, messagetype.AnimatedStickerEx:
		if source.Message == "" {
			return "", ErrNotEditable
		}
		keys = emoticonEditKeys
	case messagetype.Reply:
		keys = replyEditKeys
	default:
		return "", ErrNotEditable
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal([]byte(source.Attachment), &original); err != nil || original == nil {
		return "", ErrNotEditable
	}
	if source.Type == messagetype.Reply {
		var attachOnly bool
		if raw, ok := original["attach_only"]; ok && json.Unmarshal(raw, &attachOnly) == nil && attachOnly {
			return "", ErrNotEditable
		}
	}
	extra := make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		if raw, ok := original[key]; ok && string(raw) != "null" {
			extra[key] = raw
		}
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		return "", ErrNotEditable
	}
	return string(encoded), nil
}

// DeleteRequest deletes one own message for everyone.
type DeleteRequest struct {
	ChatID int64
	LogID  int64
}

func (r DeleteRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidChatID
	}
	if r.LogID <= 0 {
		return nil, ErrInvalidMessageID
	}
	return bson.Marshal(bson.D{{Key: "chatId", Value: r.ChatID}, {Key: "logId", Value: r.LogID}})
}
