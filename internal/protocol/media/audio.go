package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const MaxAudioBytes = 64 << 20 // mooo policy, not an official upload limit.

type AudioAttachment struct {
	Token     string `json:"k"`
	Duration  int64  `json:"d"` // Milliseconds in the observed voice memo.
	Size      int64  `json:"s"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expire"` // Milliseconds, unlike photo/video.
}

type AudioMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Attachment                      AudioAttachment
}

func (AudioMessage) String() string     { return "AudioMessage{<redacted>}" }
func (m AudioMessage) GoString() string { return m.String() }

func DecodeAudioMessage(body []byte) (AudioMessage, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return AudioMessage{}, ErrInvalidMessage
	}
	chat, err := integerField(raw, "chatId")
	if err != nil || chat <= 0 {
		return AudioMessage{}, ErrInvalidMessage
	}
	v, err := raw.LookupErr("chatLog")
	if err != nil || v.Type != bson.TypeEmbeddedDocument {
		return AudioMessage{}, ErrInvalidMessage
	}
	log := v.Document()
	typ, err := integerField(log, "type")
	if err != nil || typ != int64(messagetype.Audio) {
		return AudioMessage{}, ErrInvalidMessage
	}
	id, err := integerField(log, "logId")
	if err != nil || id <= 0 {
		return AudioMessage{}, ErrInvalidMessage
	}
	v, err = log.LookupErr("attachment")
	if err != nil || v.Type != bson.TypeString || len(v.StringValue()) > 64<<10 || !utf8.ValidString(v.StringValue()) {
		return AudioMessage{}, ErrInvalidMessage
	}
	text := v.StringValue()
	dec := json.NewDecoder(strings.NewReader(text))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return AudioMessage{}, ErrInvalidMessage
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return AudioMessage{}, ErrInvalidMessage
		}
		seen[key] = true
		var field json.RawMessage
		if dec.Decode(&field) != nil {
			return AudioMessage{}, ErrInvalidMessage
		}
	}
	if _, err = dec.Token(); err != nil {
		return AudioMessage{}, ErrInvalidMessage
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return AudioMessage{}, ErrInvalidMessage
	}
	var a AudioAttachment
	if json.Unmarshal([]byte(text), &a) != nil || !validAudioAttachment(a) {
		return AudioMessage{}, ErrInvalidMessage
	}
	return AudioMessage{ChatID: chat, LogID: id, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Attachment: a}, nil
}

func validAudioAttachment(a AudioAttachment) bool {
	return a.Token != "" && len(a.Token) <= 512 && a.Size > 0 && a.Size <= MaxAudioBytes && a.Duration >= 0 && a.Duration <= 24*60*60*1000 && a.ExpiresAt > 0 && validateDownloadURL(a.URL) == nil
}

// DownloadAudio supports observed direct-URL M4A. This attachment has no checksum;
// size and bounded ISO BMFF framing are validated, not server content authenticity.
func DownloadAudio(ctx context.Context, client *http.Client, a AudioAttachment) ([]byte, error) {
	if !validAudioAttachment(a) {
		return nil, ErrInvalidAttachment
	}
	if time.Now().UnixMilli() >= a.ExpiresAt {
		return nil, ErrExpired
	}
	data, err := downloadResource(ctx, client, a.URL, a.Size, "")
	if err != nil {
		return nil, err
	}
	if !validMP4Envelope(data) {
		return nil, ErrInvalidAttachment
	}
	return data, nil
}
