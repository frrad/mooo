package media

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// These bounds are mooo policy, not official upload limits.
const MaxVideoBytes = 64 << 20

type VideoAttachment struct {
	Token     string `json:"tk"`
	URL       string `json:"url"`
	Checksum  string `json:"cs"`
	Size      int64  `json:"s"`
	Width     int32  `json:"w"`
	Height    int32  `json:"h"`
	Duration  int64  `json:"d"` // Seconds, as stored by the official client.
	ExpiresAt int64  `json:"expire"`
	Comment   string `json:"cmt"`
}

type VideoMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Attachment                      VideoAttachment
}

func (VideoMessage) String() string     { return "VideoMessage{<redacted>}" }
func (m VideoMessage) GoString() string { return m.String() }

func DecodeVideoMessage(body []byte) (VideoMessage, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return VideoMessage{}, ErrInvalidMessage
	}
	chat, err := integerField(raw, "chatId")
	if err != nil || chat <= 0 {
		return VideoMessage{}, ErrInvalidMessage
	}
	v, err := raw.LookupErr("chatLog")
	if err != nil || v.Type != bson.TypeEmbeddedDocument {
		return VideoMessage{}, ErrInvalidMessage
	}
	log := v.Document()
	typ, err := integerField(log, "type")
	if err != nil || typ != int64(messagetype.Video) {
		return VideoMessage{}, ErrInvalidMessage
	}
	id, err := integerField(log, "logId")
	if err != nil || id <= 0 {
		return VideoMessage{}, ErrInvalidMessage
	}
	v, err = log.LookupErr("attachment")
	if err != nil || v.Type != bson.TypeString || len(v.StringValue()) > 64<<10 {
		return VideoMessage{}, ErrInvalidMessage
	}
	text := v.StringValue()
	dec := json.NewDecoder(strings.NewReader(text))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return VideoMessage{}, ErrInvalidMessage
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return VideoMessage{}, ErrInvalidMessage
		}
		seen[key] = true
		var field json.RawMessage
		if dec.Decode(&field) != nil {
			return VideoMessage{}, ErrInvalidMessage
		}
	}
	if _, err = dec.Token(); err != nil {
		return VideoMessage{}, ErrInvalidMessage
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return VideoMessage{}, ErrInvalidMessage
	}
	var a VideoAttachment
	if json.Unmarshal([]byte(text), &a) != nil || !validVideoAttachment(a) {
		return VideoMessage{}, ErrInvalidMessage
	}
	return VideoMessage{ChatID: chat, LogID: id, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Attachment: a}, nil
}

func validVideoAttachment(a VideoAttachment) bool {
	if a.Token == "" || len(a.Token) > 512 || validateDownloadURL(a.URL) != nil || a.Size <= 0 || a.Size > MaxVideoBytes || a.Width <= 0 || a.Width > 8192 || a.Height <= 0 || a.Height > 8192 || a.Duration < 0 || a.Duration > 24*60*60 || len(a.Checksum) != 40 || len(a.Comment) > 16<<10 || !utf8.ValidString(a.Comment) || strings.ContainsRune(a.Comment, 0) {
		return false
	}
	_, err := hex.DecodeString(a.Checksum)
	return err == nil
}

// DownloadVideo preserves the observed ordinary MP4 bytes. Resource-only and
// high-quality alternate resources require independent validation.
func DownloadVideo(ctx context.Context, client *http.Client, a VideoAttachment) ([]byte, error) {
	if ctx == nil || client == nil || ctx.Err() != nil {
		return nil, ErrDownload
	}
	if !validVideoAttachment(a) {
		return nil, ErrInvalidAttachment
	}
	if a.ExpiresAt > 0 && time.Now().Unix() >= a.ExpiresAt {
		return nil, ErrExpired
	}
	data, err := downloadResource(ctx, client, a.URL, a.Size, a.Checksum)
	if err != nil {
		return nil, err
	}
	if !validMP4Envelope(data) {
		return nil, ErrInvalidAttachment
	}
	return data, nil
}

// Validate bounded ISO BMFF top-level framing, not codecs or playback parity.
func validMP4Envelope(data []byte) bool {
	var ftyp, moov, mdat bool
	for offset, boxes := 0, 0; offset < len(data); boxes++ {
		if boxes >= 10000 || len(data)-offset < 8 {
			return false
		}
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		kind := string(data[offset+4 : offset+8])
		header := 8
		if size == 1 {
			if len(data)-offset < 16 {
				return false
			}
			size = binary.BigEndian.Uint64(data[offset+8:])
			header = 16
		}
		if size == 0 {
			size = uint64(len(data) - offset)
		}
		if size < uint64(header) || size > uint64(len(data)-offset) {
			return false
		}
		switch kind {
		case "ftyp":
			if offset != 0 || size < uint64(header+8) || (size-uint64(header+8))%4 != 0 {
				return false
			}
			ftyp = true
		case "moov":
			moov = size > uint64(header)
		case "mdat":
			mdat = size > uint64(header)
		}
		offset += int(size)
	}
	return ftyp && moov && mdat
}
