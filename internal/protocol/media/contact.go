package media

import (
	"bytes"
	"context"
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

const MaxContactBytes = 1 << 20 // mooo policy, not an official upload limit.

type ContactAttachment struct {
	Token     string `json:"k"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expire"` // Milliseconds, unlike photo/video.
}

type ContactMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Attachment                      ContactAttachment
}

func (ContactMessage) String() string     { return "ContactMessage{<redacted>}" }
func (m ContactMessage) GoString() string { return m.String() }

func DecodeContactMessage(body []byte) (ContactMessage, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return ContactMessage{}, ErrInvalidMessage
	}
	chat, err := integerField(raw, "chatId")
	if err != nil || chat <= 0 {
		return ContactMessage{}, ErrInvalidMessage
	}
	v, err := raw.LookupErr("chatLog")
	if err != nil || v.Type != bson.TypeEmbeddedDocument {
		return ContactMessage{}, ErrInvalidMessage
	}
	log := v.Document()
	typ, err := integerField(log, "type")
	if err != nil || typ != int64(messagetype.Contact) {
		return ContactMessage{}, ErrInvalidMessage
	}
	id, err := integerField(log, "logId")
	if err != nil || id <= 0 {
		return ContactMessage{}, ErrInvalidMessage
	}
	v, err = log.LookupErr("attachment")
	if err != nil || v.Type != bson.TypeString || len(v.StringValue()) > 64<<10 || !utf8.ValidString(v.StringValue()) {
		return ContactMessage{}, ErrInvalidMessage
	}
	text := v.StringValue()
	dec := json.NewDecoder(strings.NewReader(text))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return ContactMessage{}, ErrInvalidMessage
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return ContactMessage{}, ErrInvalidMessage
		}
		seen[key] = true
		var field json.RawMessage
		if dec.Decode(&field) != nil {
			return ContactMessage{}, ErrInvalidMessage
		}
	}
	if _, err = dec.Token(); err != nil {
		return ContactMessage{}, ErrInvalidMessage
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return ContactMessage{}, ErrInvalidMessage
	}
	var a ContactAttachment
	if json.Unmarshal([]byte(text), &a) != nil || !validContactAttachment(a) {
		return ContactMessage{}, ErrInvalidMessage
	}
	return ContactMessage{ChatID: chat, LogID: id, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Attachment: a}, nil
}

func validContactAttachment(a ContactAttachment) bool {
	return a.Token != "" && len(a.Token) <= 512 && a.Name != "" && len(a.Name) <= 512 && utf8.ValidString(a.Name) && strings.IndexFunc(a.Name, unicode.IsControl) < 0 && a.ExpiresAt > 0 && validateDownloadURL(a.URL) == nil
}

// DownloadContact preserves observed vCard bytes. Type 4 carries neither size
// nor checksum; the local byte limit and framing do not authenticate content.
func DownloadContact(ctx context.Context, client *http.Client, a ContactAttachment) ([]byte, error) {
	if !validContactAttachment(a) {
		return nil, ErrInvalidAttachment
	}
	if time.Now().UnixMilli() >= a.ExpiresAt {
		return nil, ErrExpired
	}
	data, err := downloadBoundedResource(ctx, client, a.URL, MaxContactBytes)
	if err != nil {
		return nil, err
	}
	framing := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(framing, []byte("BEGIN:VCARD\nVERSION:3.0\n")) || !bytes.HasSuffix(framing, []byte("\nEND:VCARD\n")) || bytes.Count(framing, []byte("BEGIN:VCARD")) != 1 || bytes.Count(framing, []byte("END:VCARD")) != 1 {
		return nil, ErrInvalidAttachment
	}
	return data, nil
}
