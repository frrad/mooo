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
	TextType     = messagetype.Text
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
	if r.Message == "" || !utf8.ValidString(r.Message) || len(r.Message) > maxTextBytes || strings.IndexByte(r.Message, 0) >= 0 {
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
	var err error
	if response.ChatID, err = int64Field(raw, "chatId"); err != nil || response.ChatID <= 0 {
		return WriteResponse{}, ErrInvalidWriteResp
	}
	if response.LogID, err = int64Field(raw, "logId"); err != nil || response.LogID <= 0 {
		return WriteResponse{}, ErrInvalidWriteResp
	}
	for key, target := range map[string]*int64{
		"msgId": &response.MessageID, "prevId": &response.PrevID, "sendAt": &response.SendAt,
	} {
		if value, lookupErr := raw.LookupErr(key); lookupErr == nil {
			parsed, parseErr := int64Value(value)
			if parseErr != nil {
				return WriteResponse{}, ErrInvalidWriteResp
			}
			*target = parsed
		}
	}
	if value, lookupErr := raw.LookupErr("chatLog"); lookupErr == nil {
		if value.Type != bson.TypeEmbeddedDocument {
			return WriteResponse{}, ErrInvalidWriteResp
		}
		response.ChatLog = append(bson.Raw(nil), value.Document()...)
		if err := response.ChatLog.Validate(); err != nil {
			return WriteResponse{}, ErrInvalidWriteResp
		}
	}
	return response, nil
}

func int64Value(value bson.RawValue) (int64, error) {
	switch value.Type {
	case bson.TypeInt64:
		return value.Int64(), nil
	case bson.TypeInt32:
		return int64(value.Int32()), nil
	default:
		return 0, ErrInvalidWriteResp
	}
}
