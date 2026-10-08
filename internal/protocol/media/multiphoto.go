package media

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	MaxAlbumPhotos = 30
	MaxAlbumBytes  = 64 << 20
)

// MultiPhotoMessage keeps the observed parallel arrays in their original order.
// Resource-only albums without full image URLs remain unsupported by this path.
type MultiPhotoMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Photos                          []PhotoAttachment
	Comments                        []string
}

func (MultiPhotoMessage) String() string     { return "MultiPhotoMessage{<redacted>}" }
func (m MultiPhotoMessage) GoString() string { return m.String() }

func DecodeMultiPhotoMessage(body []byte) (MultiPhotoMessage, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	chatID, err := integerField(raw, "chatId")
	if err != nil || chatID <= 0 {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	value, err := raw.LookupErr("chatLog")
	if err != nil || value.Type != bson.TypeEmbeddedDocument {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	log := value.Document()
	typ, err := integerField(log, "type")
	if err != nil || typ != int64(messagetype.MultiPhoto) {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	logID, err := integerField(log, "logId")
	if err != nil || logID <= 0 {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	value, err = log.LookupErr("attachment")
	if err != nil || value.Type != bson.TypeString || len(value.StringValue()) > 128<<10 {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	attachmentText := value.StringValue()
	dec := json.NewDecoder(strings.NewReader(attachmentText))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return MultiPhotoMessage{}, ErrInvalidMessage
		}
		seen[key] = true
		var field json.RawMessage
		if dec.Decode(&field) != nil {
			return MultiPhotoMessage{}, ErrInvalidMessage
		}
	}
	if _, err = dec.Token(); err != nil {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	var a struct {
		Comments   []string `json:"cmtl"`
		Keys       []string `json:"kl"`
		Widths     []int32  `json:"wl"`
		Heights    []int32  `json:"hl"`
		Sizes      []int64  `json:"sl"`
		Checksums  []string `json:"csl"`
		MIMEs      []string `json:"mtl"`
		URLs       []string `json:"imageUrls"`
		Thumbnails []string `json:"thumbnailUrls"`
		ExpiresAt  int64    `json:"expire"`
	}
	if json.Unmarshal([]byte(value.StringValue()), &a) != nil {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	n := len(a.URLs)
	if n < 2 || n > MaxAlbumPhotos || len(a.Keys) != n || len(a.Widths) != n || len(a.Heights) != n || len(a.Sizes) != n || len(a.Checksums) != n || len(a.MIMEs) != n || len(a.Thumbnails) != n || a.Comments != nil && len(a.Comments) != n {
		return MultiPhotoMessage{}, ErrInvalidMessage
	}
	photos := make([]PhotoAttachment, n)
	comments := make([]string, n)
	for i, c := range a.Comments {
		if len(c) > 16<<10 || !utf8.ValidString(c) || strings.ContainsRune(c, 0) {
			return MultiPhotoMessage{}, ErrInvalidMessage
		}
		comments[i] = c
	}
	var total int64
	for i := range n {
		p := PhotoAttachment{Key: a.Keys[i], Width: a.Widths[i], Height: a.Heights[i], Size: a.Sizes[i], Checksum: a.Checksums[i], MediaType: a.MIMEs[i], URL: a.URLs[i], ThumbnailURL: a.Thumbnails[i], ExpiresAt: a.ExpiresAt}
		if p.Key == "" || len(p.Key) > 512 || p.Width <= 0 || p.Height <= 0 || p.Width > 4096 || p.Height > 4096 || p.Size <= 0 || p.Size > MaxImageBytes || len(p.Checksum) != 40 || (p.MediaType != "image/png" && p.MediaType != "image/jpeg" && p.MediaType != "image/jpg") || validateDownloadURL(p.URL) != nil || validateDownloadURL(p.ThumbnailURL) != nil {
			return MultiPhotoMessage{}, ErrInvalidMessage
		}
		if _, err := hex.DecodeString(p.Checksum); err != nil {
			return MultiPhotoMessage{}, ErrInvalidMessage
		}
		p.Checksum = strings.ToUpper(p.Checksum)
		total += p.Size
		if total > MaxAlbumBytes {
			return MultiPhotoMessage{}, ErrInvalidMessage
		}
		photos[i] = p
	}
	return MultiPhotoMessage{ChatID: chatID, LogID: logID, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Photos: photos, Comments: comments}, nil
}

// DownloadAlbumPhoto additionally bounds the actual image canvas, not just metadata.
func DownloadAlbumPhoto(ctx context.Context, client *http.Client, photo PhotoAttachment) ([]byte, error) {
	data, err := DownloadPhoto(ctx, client, photo)
	if err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 {
		return nil, ErrInvalidAttachment
	}
	return data, nil
}
