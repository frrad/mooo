// Package media contains transport-independent Kakao chat-media messages.
package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	ShipCommand     = "SHIP"
	PostCommand     = "POST"
	CompleteCommand = "COMPLETE"
	PhotoType       = messagetype.Photo
	MaxImageBytes   = 16 << 20
)

var (
	ErrInvalidImage      = errors.New("media: invalid image")
	ErrUnsupportedImage  = errors.New("media: unsupported image format")
	ErrInvalidResponse   = errors.New("media: invalid response")
	ErrInvalidOffset     = errors.New("media: invalid upload offset")
	ErrInvalidMessage    = errors.New("media: invalid photo message")
	ErrUnsafeURL         = errors.New("media: unsafe download URL")
	ErrDownload          = errors.New("media: download failed")
	ErrExpired           = errors.New("media: attachment expired")
	ErrInvalidAttachment = errors.New("media: invalid attachment")
	ErrChecksumMismatch  = errors.New("media: attachment checksum mismatch")
	// ErrUnavailable reports a media server refusal (403, 404 or 410) for a
	// resource that is no longer served. Retrying cannot recover it.
	ErrUnavailable = errors.New("media: attachment unavailable")
	// ErrInvalidCaption reports a photo caption outside the accepted bounds.
	ErrInvalidCaption = errors.New("media: invalid photo caption")
)

// MaxCaptionBytes bounds a photo caption in either direction.
const MaxCaptionBytes = 16 << 10

// ValidCaption reports whether a photo caption is bounded valid UTF-8
// without NUL characters. The empty caption is valid.
func ValidCaption(caption string) bool {
	return len(caption) <= MaxCaptionBytes && utf8.ValidString(caption) && !strings.ContainsRune(caption, 0)
}

// unavailableStatus reports media server statuses that mean the resource is
// gone rather than temporarily unreachable.
func unavailableStatus(code int) bool {
	return code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusGone
}

// Image is a validated, bounded upload prepared from caller-owned bytes.
type Image struct {
	Data      []byte
	Extension string
	Width     int32
	Height    int32
	Checksum  string
}

type PhotoAttachment struct {
	Key          string `json:"k"`
	Width        int32  `json:"w"`
	Height       int32  `json:"h"`
	Size         int64  `json:"s"`
	Checksum     string `json:"cs"`
	MediaType    string `json:"mt"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnailUrl"`
	ExpiresAt    int64  `json:"expire"` // Epoch milliseconds (observed).
	// Comment is the sender's optional photo caption.
	Comment string `json:"cmt"`
}

type PhotoMessage struct {
	ChatID     int64
	LogID      int64
	AuthorID   int64
	SentAt     int64
	Attachment PhotoAttachment
}

// DecodePhotoMessage parses a type-2 MSG without logging its ephemeral URLs or
// media key. Unknown attachment fields are retained by neither this decoder nor
// its callers, allowing the wire schema to grow safely.
func DecodePhotoMessage(body []byte) (PhotoMessage, error) {
	raw := bson.Raw(body)
	chatID, err := integerField(raw, "chatId")
	if err != nil || chatID <= 0 {
		return PhotoMessage{}, ErrInvalidMessage
	}
	logValue, err := raw.LookupErr("chatLog")
	if err != nil || logValue.Type != bson.TypeEmbeddedDocument {
		return PhotoMessage{}, ErrInvalidMessage
	}
	log := logValue.Document()
	messageType, err := integerField(log, "type")
	if err != nil || messageType != int64(PhotoType) {
		return PhotoMessage{}, ErrInvalidMessage
	}
	logID, err := integerField(log, "logId")
	if err != nil || logID <= 0 {
		return PhotoMessage{}, ErrInvalidMessage
	}
	attachmentValue, err := log.LookupErr("attachment")
	if err != nil || attachmentValue.Type != bson.TypeString || len(attachmentValue.StringValue()) > 64*1024 {
		return PhotoMessage{}, ErrInvalidMessage
	}
	var attachment PhotoAttachment
	if err := json.Unmarshal([]byte(attachmentValue.StringValue()), &attachment); err != nil {
		return PhotoMessage{}, ErrInvalidMessage
	}
	if attachment.Key == "" || attachment.Width <= 0 || attachment.Height <= 0 || attachment.Size <= 0 || attachment.Size > MaxImageBytes ||
		len(attachment.Checksum) != 40 || (attachment.MediaType != "image/jpg" && attachment.MediaType != "image/jpeg" && attachment.MediaType != "image/png") ||
		validateDownloadURL(attachment.URL) != nil || validateDownloadURL(attachment.ThumbnailURL) != nil {
		return PhotoMessage{}, ErrInvalidMessage
	}
	if _, err := hex.DecodeString(attachment.Checksum); err != nil {
		return PhotoMessage{}, ErrInvalidMessage
	}
	if !ValidCaption(attachment.Comment) {
		return PhotoMessage{}, ErrInvalidMessage
	}
	return PhotoMessage{
		ChatID: chatID, LogID: logID,
		AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"),
		Attachment: attachment,
	}, nil
}

// DownloadPhoto fetches a decoded attachment with a strict size bound and
// verifies both the advertised byte length and SHA-1 checksum. Redirect targets
// are validated before the client follows them.
func DownloadPhoto(ctx context.Context, client *http.Client, attachment PhotoAttachment) ([]byte, error) {
	if ctx == nil || client == nil || ctx.Err() != nil {
		return nil, ErrDownload
	}
	if attachment.Size <= 0 || attachment.Size > MaxImageBytes {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAttachment, ErrDownload)
	}
	if attachment.MediaType != "image/jpg" && attachment.MediaType != "image/jpeg" && attachment.MediaType != "image/png" {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAttachment, ErrDownload)
	}
	if attachment.ExpiresAt > 0 && time.Now().UnixMilli() >= attachment.ExpiresAt {
		return nil, fmt.Errorf("%w: %w", ErrExpired, ErrDownload)
	}
	if validateDownloadURL(attachment.URL) != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsafeURL, ErrDownload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.URL, nil)
	if err != nil {
		return nil, ErrDownload
	}
	redirectClient := *client
	previousRedirectPolicy := client.CheckRedirect
	redirectClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if validateDownloadURL(req.URL.String()) != nil {
			return fmt.Errorf("%w: %w", ErrUnsafeURL, ErrDownload)
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(req, via)
		}
		if len(via) >= 10 {
			return errors.New("media: too many redirects")
		}
		return nil
	}
	resp, err := redirectClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && validateDownloadURL(resp.Request.URL.String()) != nil {
		return nil, ErrDownload
	}
	if unavailableStatus(resp.StatusCode) {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, ErrDownload)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, attachment.Size+1))
	if err != nil {
		return nil, ErrDownload
	}
	if int64(len(data)) < attachment.Size {
		return nil, ErrDownload
	}
	if int64(len(data)) > attachment.Size {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAttachment, ErrDownload)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (attachment.MediaType == "image/png" && format != "png") || attachment.MediaType != "image/png" && format != "jpeg" {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedImage, ErrDownload)
	}
	sum := sha1.Sum(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), attachment.Checksum) {
		return nil, fmt.Errorf("%w: %w", ErrChecksumMismatch, ErrDownload)
	}
	return data, nil
}

func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Path == "" {
		return ErrUnsafeURL
	}
	host := strings.ToLower(u.Hostname())
	if host != "talk.kakaocdn.net" && !strings.HasSuffix(host, ".talk.kakao.com") {
		return ErrUnsafeURL
	}
	return nil
}

// PrepareImage accepts JPEG and PNG data, derives dimensions, and computes the
// uppercase SHA-1 checksum used by the SHIP reservation request.
func PrepareImage(data []byte) (Image, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return Image{}, ErrInvalidImage
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 1<<20 || config.Height > 1<<20 {
		return Image{}, ErrInvalidImage
	}
	ext := ""
	switch format {
	case "jpeg":
		ext = "jpg"
	case "png":
		ext = "png"
	default:
		return Image{}, ErrUnsupportedImage
	}
	sum := sha1.Sum(data)
	return Image{
		Data: append([]byte(nil), data...), Extension: ext,
		Width: int32(config.Width), Height: int32(config.Height),
		Checksum: strings.ToUpper(hex.EncodeToString(sum[:])),
	}, nil
}

type ShipRequest struct {
	ChatID int64
	Image  Image
}

func (r ShipRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || len(r.Image.Data) == 0 || r.Image.Extension == "" || r.Image.Checksum == "" {
		return nil, ErrInvalidImage
	}
	return bson.Marshal(bson.D{
		{Key: "c", Value: r.ChatID}, {Key: "t", Value: PhotoType},
		{Key: "s", Value: int64(len(r.Image.Data))}, {Key: "cs", Value: r.Image.Checksum},
		{Key: "e", Value: r.Image.Extension}, {Key: "ex", Value: "{}"},
	})
}

type ShipResponse struct {
	Key  string
	Host string
	Port int
}

func DecodeShipResponse(body []byte) (ShipResponse, error) {
	raw := bson.Raw(body)
	key, err := stringField(raw, "k")
	if err != nil {
		return ShipResponse{}, ErrInvalidResponse
	}
	host, err := stringField(raw, "vh")
	if err != nil {
		return ShipResponse{}, ErrInvalidResponse
	}
	port64, err := integerField(raw, "p")
	if err != nil || port64 <= 0 || port64 > 65535 {
		return ShipResponse{}, ErrInvalidResponse
	}
	return ShipResponse{Key: key, Host: host, Port: int(port64)}, nil
}

type PostRequest struct {
	UserID     int64
	Key        string
	ChatID     int64
	Image      Image
	AppVersion string
	MediaID    int64
	// Comment is the optional photo caption, sent as cmt in the extra JSON.
	Comment string
}

func (r PostRequest) MarshalBSON() ([]byte, error) {
	if r.UserID <= 0 || r.ChatID <= 0 || r.Key == "" || r.AppVersion == "" || r.MediaID <= 0 || len(r.Image.Data) == 0 {
		return nil, ErrInvalidImage
	}
	if !ValidCaption(r.Comment) {
		return nil, ErrInvalidCaption
	}
	extra := "{}"
	if r.Comment != "" {
		encoded, err := json.Marshal(struct {
			Comment string `json:"cmt"`
		}{r.Comment})
		if err != nil {
			return nil, ErrInvalidCaption
		}
		extra = string(encoded)
	}
	return bson.Marshal(bson.D{
		{Key: "u", Value: r.UserID}, {Key: "k", Value: r.Key}, {Key: "t", Value: PhotoType},
		{Key: "s", Value: int64(len(r.Image.Data))}, {Key: "c", Value: r.ChatID}, {Key: "mid", Value: r.MediaID},
		{Key: "w", Value: r.Image.Width}, {Key: "h", Value: r.Image.Height},
		{Key: "mm", Value: "99999"}, {Key: "nt", Value: int32(0)}, {Key: "os", Value: "mac"},
		{Key: "av", Value: r.AppVersion}, {Key: "ex", Value: extra}, {Key: "ns", Value: false},
		{Key: "dt", Value: int32(4)}, {Key: "scp", Value: int32(1)},
	})
}

func DecodePostOffset(body []byte, size int) (int, error) {
	raw := bson.Raw(body)
	value, err := raw.LookupErr("o")
	if err != nil {
		return 0, nil
	}
	offset64, err := integerValue(value)
	if err != nil || offset64 < 0 || offset64 > int64(size) {
		return 0, ErrInvalidOffset
	}
	return int(offset64), nil
}

// SendResult retains the server's complete chatLog document. Its schema grows
// independently, so callers can decode additional fields without losing data.
type SendResult struct {
	ChatLog bson.Raw
}

// SendResultPosition extracts the durable identity from a completed image
// send. It deliberately requires both fields so callers cannot persist an
// ambiguous response as a successful bridge message.
func SendResultPosition(result SendResult) (logID, sendAt int64, err error) {
	if result.ChatLog == nil {
		return 0, 0, ErrInvalidResponse
	}
	logID, err = integerField(result.ChatLog, "logId")
	if err != nil || logID <= 0 {
		return 0, 0, ErrInvalidResponse
	}
	sendAt, err = integerField(result.ChatLog, "sendAt")
	if err != nil || sendAt <= 0 {
		return 0, 0, ErrInvalidResponse
	}
	return logID, sendAt, nil
}

func DecodeComplete(body []byte) (SendResult, error) {
	raw := bson.Raw(body)
	value, err := raw.LookupErr("chatLog")
	if err != nil || value.Type != bson.TypeEmbeddedDocument {
		return SendResult{}, ErrInvalidResponse
	}
	chatLog := append(bson.Raw(nil), value.Document()...)
	if err := chatLog.Validate(); err != nil {
		return SendResult{}, ErrInvalidResponse
	}
	return SendResult{ChatLog: chatLog}, nil
}

func stringField(raw bson.Raw, key string) (string, error) {
	v, err := raw.LookupErr(key)
	if err != nil || v.Type != bson.TypeString || v.StringValue() == "" {
		return "", ErrInvalidResponse
	}
	return v.StringValue(), nil
}

func integerField(raw bson.Raw, key string) (int64, error) {
	v, err := raw.LookupErr(key)
	if err != nil {
		return 0, err
	}
	return integerValue(v)
}

func optionalInt64(raw bson.Raw, key string) int64 {
	value, err := integerField(raw, key)
	if err != nil {
		return 0
	}
	return value
}

func integerValue(v bson.RawValue) (int64, error) {
	switch v.Type {
	case bson.TypeInt32:
		return int64(v.Int32()), nil
	case bson.TypeInt64:
		return v.Int64(), nil
	default:
		return 0, fmt.Errorf("%w: integer type", ErrInvalidResponse)
	}
}
