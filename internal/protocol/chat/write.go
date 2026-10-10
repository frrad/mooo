package chat

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	WriteCommand = "WRITE"
)

var (
	ErrInvalidChatID    = errors.New("chat: invalid chat id")
	ErrInvalidMessageID = errors.New("chat: invalid message id")
	ErrInvalidMessage   = errors.New("chat: invalid message")
	ErrInvalidWriteType = errors.New("chat: invalid WRITE type")
	ErrInvalidWriteResp = errors.New("chat: invalid WRITE response")
)

const maxTextBytes = 64 * 1024

// WriteRequest is the current Mac client's generic WRITE request. Optional
// object properties are omitted when empty, matching its serializer.
type WriteRequest struct {
	ChatID      int64
	MessageID   int64
	Scope       int32
	ThreadID    int64
	Message     string
	Type        int32
	NoSeen      bool
	NoLight     bool
	Extra       string
	Supplement  string
	Silence     bool
	FeatureStat string
}

func (r WriteRequest) Validate() error {
	if r.ChatID <= 0 {
		return ErrInvalidChatID
	}
	if r.MessageID < 0 {
		return ErrInvalidMessageID
	}
	if r.Type <= 0 {
		return ErrInvalidWriteType
	}
	// An album (type 27) has an empty msg; its photos are listed in extra.
	emptyAllowed := r.Type == messagetype.MultiPhoto && r.Extra != ""
	if (r.Message == "" && !emptyAllowed) || !utf8.ValidString(r.Message) || len(r.Message) > maxTextBytes || strings.IndexByte(r.Message, 0) >= 0 {
		return ErrInvalidMessage
	}
	for _, value := range []string{r.Extra, r.Supplement, r.FeatureStat} {
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return ErrInvalidMessage
		}
	}
	return nil
}

func (r WriteRequest) MarshalBSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	doc := bson.D{
		{Key: "chatId", Value: r.ChatID},
		{Key: "msg", Value: r.Message},
		{Key: "type", Value: r.Type},
		{Key: "noSeen", Value: r.NoSeen},
	}
	if r.MessageID > 0 {
		doc = append(doc, bson.E{Key: "msgId", Value: r.MessageID})
	}
	if r.Scope != 0 {
		doc = append(doc, bson.E{Key: "scope", Value: r.Scope})
	}
	if r.ThreadID != 0 {
		doc = append(doc, bson.E{Key: "threadId", Value: r.ThreadID})
	}
	if r.NoLight {
		doc = append(doc, bson.E{Key: "noLight", Value: true})
	}
	if r.Extra != "" {
		doc = append(doc, bson.E{Key: "extra", Value: r.Extra})
	}
	if r.Supplement != "" {
		doc = append(doc, bson.E{Key: "supplement", Value: r.Supplement})
	}
	if r.Silence {
		doc = append(doc, bson.E{Key: "silence", Value: true})
	}
	if r.FeatureStat != "" {
		doc = append(doc, bson.E{Key: "featureStat", Value: r.FeatureStat})
	}
	out, err := bson.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("chat: encode WRITE: %w", err)
	}
	return out, nil
}

// WriteResponse exposes stable correlation and ordering fields while retaining
// an optional full chatLog document returned by some server versions.
type WriteResponse struct {
	MessageID int64
	ChatID    int64
	LogID     int64
	PrevID    int64
	SendAt    int64
	ChatLog   bson.Raw
}

func DecodeWriteResponse(body []byte) (WriteResponse, error) {
	raw := bson.Raw(body)
	response := WriteResponse{}
	var ok bool
	var err error
	if response.ChatID, ok, err = fields.Int64(raw, "chatId"); err != nil || !ok || response.ChatID <= 0 {
		return WriteResponse{}, ErrInvalidWriteResp
	}
	if response.LogID, ok, err = fields.Int64(raw, "logId"); err != nil || !ok || response.LogID <= 0 {
		return WriteResponse{}, ErrInvalidWriteResp
	}
	for key, target := range map[string]*int64{
		"msgId": &response.MessageID, "prevId": &response.PrevID, "sendAt": &response.SendAt,
	} {
		if *target, _, err = fields.Int64(raw, key); err != nil {
			return WriteResponse{}, ErrInvalidWriteResp
		}
	}
	if response.ChatLog, _, err = fields.Document(raw, "chatLog"); err != nil {
		return WriteResponse{}, ErrInvalidWriteResp
	}
	return response, nil
}
