package media

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/chatlog"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"github.com/frrad/mooo/internal/protocol/strictjson"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const MaxFileBytes = 64 << 20 // mooo policy, not an official upload limit.

type FileAttachment struct {
	Token     string `json:"k"`
	Name      string `json:"name"`
	Size      int64  `json:"s"`
	URL       string `json:"url"`
	Checksum  string `json:"cs"`
	ExpiresAt int64  `json:"expire"` // Milliseconds, unlike photo/video.
}

type FileMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Attachment                      FileAttachment
}

func (FileMessage) String() string     { return "FileMessage{<redacted>}" }
func (m FileMessage) GoString() string { return m.String() }

func DecodeFileMessage(env chatlog.Envelope) (FileMessage, error) {
	if env.Type != messagetype.File || !env.LogIDInChatLog {
		return FileMessage{}, ErrInvalidMessage
	}
	log := env.Log
	v, err := log.LookupErr("attachment")
	if err != nil || v.Type != bson.TypeString || !utf8.ValidString(v.StringValue()) {
		return FileMessage{}, ErrInvalidMessage
	}
	text := v.StringValue()
	var a FileAttachment
	if strictjson.Unmarshal([]byte(text), &a, attachmentJSON) != nil || !validFileAttachment(a) {
		return FileMessage{}, ErrInvalidMessage
	}
	return FileMessage{ChatID: env.ChatID, LogID: env.LogID, AuthorID: env.AuthorID, SentAt: env.SentAt, Attachment: a}, nil
}

func validFileAttachment(a FileAttachment) bool {
	if a.Token == "" || len(a.Token) > 512 || a.Name == "" || len(a.Name) > 512 || !utf8.ValidString(a.Name) || a.Name == "." || a.Name == ".." || strings.ContainsAny(a.Name, "/\\") || strings.IndexFunc(a.Name, unicode.IsControl) >= 0 || a.Size <= 0 || a.Size > MaxFileBytes || a.ExpiresAt <= 0 || validateDownloadURL(a.URL) != nil || len(a.Checksum) != 40 {
		return false
	}
	_, err := hex.DecodeString(a.Checksum)
	return err == nil
}

func DownloadFile(ctx context.Context, client *http.Client, a FileAttachment) ([]byte, error) {
	if !validFileAttachment(a) {
		return nil, ErrInvalidAttachment
	}
	if time.Now().UnixMilli() >= a.ExpiresAt {
		return nil, ErrExpired
	}
	return downloadResource(ctx, client, a.URL, a.Size, a.Checksum)
}
