package media

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/messagetype"
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

func DecodeFileMessage(body []byte) (FileMessage, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return FileMessage{}, ErrInvalidMessage
	}
	chat, err := integerField(raw, "chatId")
	if err != nil || chat <= 0 {
		return FileMessage{}, ErrInvalidMessage
	}
	v, err := raw.LookupErr("chatLog")
	if err != nil || v.Type != bson.TypeEmbeddedDocument {
		return FileMessage{}, ErrInvalidMessage
	}
	log := v.Document()
	typ, err := integerField(log, "type")
	if err != nil || typ != int64(messagetype.File) {
		return FileMessage{}, ErrInvalidMessage
	}
	id, err := integerField(log, "logId")
	if err != nil || id <= 0 {
		return FileMessage{}, ErrInvalidMessage
	}
	v, err = log.LookupErr("attachment")
	if err != nil || v.Type != bson.TypeString || len(v.StringValue()) > 64<<10 || !utf8.ValidString(v.StringValue()) {
		return FileMessage{}, ErrInvalidMessage
	}
	text := v.StringValue()
	dec := json.NewDecoder(strings.NewReader(text))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return FileMessage{}, ErrInvalidMessage
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return FileMessage{}, ErrInvalidMessage
		}
		seen[key] = true
		var field json.RawMessage
		if dec.Decode(&field) != nil {
			return FileMessage{}, ErrInvalidMessage
		}
	}
	if _, err = dec.Token(); err != nil {
		return FileMessage{}, ErrInvalidMessage
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return FileMessage{}, ErrInvalidMessage
	}
	var a FileAttachment
	if json.Unmarshal([]byte(text), &a) != nil || !validFileAttachment(a) {
		return FileMessage{}, ErrInvalidMessage
	}
	return FileMessage{ChatID: chat, LogID: id, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Attachment: a}, nil
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
