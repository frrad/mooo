package media

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// DirectPhotoWriteRequest matches the Mac WRITEMEDIA photo serializer. Resource
// metadata is a nested BSON object; the caption remains the JSON extra string.
type DirectPhotoWriteRequest struct {
	ChatID, MessageID int64
	Image             Image
	Ticket            DirectTicket
	Caption           string
}

func (r DirectPhotoWriteRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || r.MessageID < 0 || r.Image.Width <= 0 || r.Image.Height <= 0 || !r.Ticket.writable() {
		return nil, ErrInvalidImage
	}
	if !ValidCaption(r.Caption) {
		return nil, ErrInvalidCaption
	}
	return directWrite(r.ChatID, r.MessageID, messagetype.Photo, "photo", bson.D{
		{Key: "resource", Value: r.Ticket.Resource}, {Key: "resourceType", Value: r.Ticket.ResourceType},
		{Key: "hfac", Value: r.Ticket.UploadKey}, {Key: "width", Value: r.Image.Width}, {Key: "height", Value: r.Image.Height},
	}, r.Caption)
}

// DirectUploadWriteRequest is the WRITEMEDIA for a file or video sent over
// the HTTP upload. A file adds its name and lowercase SHA-1 checksum to the
// resource; a video adds nothing and carries its caption in extra
// (research/bridge/SHIP-REDIRECT-FILE-VIDEO.md).
type DirectUploadWriteRequest struct {
	ChatID, MessageID int64
	Upload            Upload
	Ticket            DirectTicket
}

func (r DirectUploadWriteRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || r.MessageID < 0 || !r.Upload.valid() || !r.Ticket.writable() {
		return nil, ErrInvalidUpload
	}
	media := bson.D{{Key: "resource", Value: r.Ticket.Resource}, {Key: "resourceType", Value: r.Ticket.ResourceType}, {Key: "hfac", Value: r.Ticket.UploadKey}}
	switch r.Upload.Type {
	case messagetype.Video:
		if !ValidCaption(r.Upload.Comment) {
			return nil, ErrInvalidCaption
		}
		return directWrite(r.ChatID, r.MessageID, messagetype.Video, "video", media, r.Upload.Comment)
	case messagetype.File:
		if r.Upload.Comment != "" {
			return nil, ErrCaptionNotSupported
		}
		media = append(media, bson.E{Key: "name", Value: r.Upload.Name}, bson.E{Key: "checksum", Value: strings.ToLower(r.Upload.Checksum)})
		return directWrite(r.ChatID, r.MessageID, messagetype.File, "file", media, "")
	}
	return nil, ErrInvalidUpload
}

func (t DirectTicket) writable() bool {
	return safeDirectValue(t.Resource) && safeDirectValue(t.UploadKey) && t.ResourceType == "vk"
}

func directWrite(chatID, messageID int64, messageType int32, mediaKey string, media bson.D, caption string) ([]byte, error) {
	doc := bson.D{{Key: "chatId", Value: chatID}, {Key: "msgId", Value: messageID}, {Key: "scope", Value: int32(1)}, {Key: "type", Value: messageType}, {Key: "noSeen", Value: false}, {Key: "noLight", Value: false}, {Key: "silence", Value: false}, {Key: mediaKey, Value: media}}
	if caption != "" {
		extra, _ := json.Marshal(map[string]string{"cmt": caption})
		doc = append(doc, bson.E{Key: "extra", Value: string(extra)})
	}
	return bson.Marshal(doc)
}

// DecodeDirectWrite reads the WRITEMEDIA reply for chatID and requires a
// durable message position.
func DecodeDirectWrite(body []byte, chatID int64) (SendResult, error) {
	raw := bson.Raw(body)
	id, present, err := fields.Int64(raw, "chatId")
	if err != nil || !present || id != chatID {
		return SendResult{}, invalidResponse(WriteMediaCommand, "chatId", body)
	}
	log, ok, err := fields.Document(raw, "chatLog")
	if err != nil {
		return SendResult{}, invalidResponse(WriteMediaCommand, "chatLog_type", body)
	}
	if !ok {
		log = raw
	}
	result := SendResult{ChatLog: bytes.Clone(log)}
	if _, _, err = SendResultPosition(result); err != nil {
		return SendResult{}, invalidResponse(WriteMediaCommand, "position", body)
	}
	return result, nil
}
