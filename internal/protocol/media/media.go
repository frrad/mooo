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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/chatlog"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	ShipCommand     = "SHIP"
	PostCommand     = "POST"
	CompleteCommand = "COMPLETE"
	MaxImageBytes   = 16 << 20
)

var (
	ErrInvalidImage     = errors.New("media: invalid image")
	ErrUnsupportedImage = errors.New("media: unsupported image format")
	ErrInvalidResponse  = errors.New("media: invalid response")
	// ErrUploadRedirect means an alternate upload strategy is required. The
	// trailer path returns it before uploading; unsupported HTTP strategies
	// also return it without attempting a chat message.
	ErrUploadRedirect    = errors.New("media: alternate upload required")
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

// InvalidMessageError names the check that a media message failed. Reason is
// a fixed code chosen by the decoder, never a value from the message, so it is
// safe to log.
type InvalidMessageError struct {
	Reason string
}

func (e *InvalidMessageError) Error() string { return ErrInvalidMessage.Error() + ": " + e.Reason }
func (e *InvalidMessageError) Unwrap() error { return ErrInvalidMessage }

func invalidMessage(reason string) error { return &InvalidMessageError{Reason: reason} }

// ResponseError reports a media reply that failed to decode. Reason is a fixed
// code naming the failed check and Shape lists the reply's keys and BSON types;
// both are safe to log. The raw reply, which can hold an upload token, is kept
// out of Error and is available only through RawBody for a private dump.
type ResponseError struct {
	Command string
	Reason  string
	Shape   string
	body    []byte
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("%s: %s %s, reply %s", ErrInvalidResponse, e.Command, e.Reason, e.Shape)
}
func (e *ResponseError) Unwrap() error { return ErrInvalidResponse }

// RawBody returns a copy of the reply. It is private: never log it.
func (e *ResponseError) RawBody() []byte { return bytes.Clone(e.body) }

func invalidResponse(command, reason string, body []byte) error {
	return &ResponseError{Command: command, Reason: reason, Shape: bsonfield.Shape(body), body: bytes.Clone(body)}
}

// fieldReason names how a required reply field failed: wrong type, missing,
// or present but empty or out of range.
func fieldReason(key string, present bool, err error) string {
	switch {
	case err != nil:
		return key + "_type"
	case !present:
		return key + "_missing"
	}
	return key + "_value"
}

// checksumLengthReason reports a wrong checksum length. The length tells a
// hash algorithm apart without revealing the checksum; longer values are
// grouped so the code stays bounded.
func checksumLengthReason(n int) string {
	if n > 128 {
		return "checksum_length_long"
	}
	return "checksum_length_" + strconv.Itoa(n)
}

// fields reads response fields; callers map every failure to their own
// response error.
var fields = bsonfield.Reader{Err: ErrInvalidResponse}

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
func DecodePhotoMessage(env chatlog.Envelope) (PhotoMessage, error) {
	if env.Type != messagetype.Photo {
		return PhotoMessage{}, invalidMessage("type")
	}
	if !env.LogIDInChatLog {
		return PhotoMessage{}, invalidMessage("log_id_location")
	}
	log := env.Log
	attachmentValue, err := log.LookupErr("attachment")
	switch {
	case err != nil:
		return PhotoMessage{}, invalidMessage("attachment_missing")
	case attachmentValue.Type != bson.TypeString:
		return PhotoMessage{}, invalidMessage("attachment_type")
	case len(attachmentValue.StringValue()) > 64*1024:
		return PhotoMessage{}, invalidMessage("attachment_size")
	}
	var attachment PhotoAttachment
	if err := json.Unmarshal([]byte(attachmentValue.StringValue()), &attachment); err != nil {
		return PhotoMessage{}, invalidMessage("attachment_json")
	}
	switch {
	case attachment.Key == "":
		return PhotoMessage{}, invalidMessage("key")
	case attachment.Width <= 0 || attachment.Height <= 0:
		return PhotoMessage{}, invalidMessage("dimensions")
	case attachment.Size <= 0 || attachment.Size > MaxImageBytes:
		return PhotoMessage{}, invalidMessage("size")
	// The Mac client models no photo checksum, and photos sent from the
	// owner's Android phone carry an empty cs. A present cs must be SHA-1 hex.
	case attachment.Checksum != "" && len(attachment.Checksum) != 40:
		return PhotoMessage{}, invalidMessage(checksumLengthReason(len(attachment.Checksum)))
	case photoFormat(attachment.MediaType) == "":
		return PhotoMessage{}, invalidMessage("media_type")
	}
	if problem := downloadURLProblem(attachment.URL); problem != "" {
		return PhotoMessage{}, invalidMessage("url_" + problem)
	}
	if problem := downloadURLProblem(attachment.ThumbnailURL); problem != "" {
		return PhotoMessage{}, invalidMessage("thumbnail_url_" + problem)
	}
	if _, err := hex.DecodeString(attachment.Checksum); err != nil {
		return PhotoMessage{}, invalidMessage("checksum_hex")
	}
	if !ValidCaption(attachment.Comment) {
		return PhotoMessage{}, invalidMessage("caption")
	}
	return PhotoMessage{
		ChatID: env.ChatID, LogID: env.LogID,
		AuthorID: env.AuthorID, SentAt: env.SentAt,
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
	format := photoFormat(attachment.MediaType)
	if format == "" {
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
	if _, _, err := photoSize(data, format); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedImage, ErrDownload)
	}
	// Without a checksum, the size bound and image decoding above are the
	// integrity checks, as on the Mac client's download path.
	sum := sha1.Sum(data)
	if attachment.Checksum != "" && !strings.EqualFold(hex.EncodeToString(sum[:]), attachment.Checksum) {
		return nil, fmt.Errorf("%w: %w", ErrChecksumMismatch, ErrDownload)
	}
	return data, nil
}

// photoFormat names the image format of an accepted Kakao photo media type,
// or returns "" for an unsupported one. Kakao labels JPEG photos "image/jpg";
// newer clients may send WebP.
func photoFormat(mediaType string) string {
	switch mediaType {
	case "image/jpg", "image/jpeg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	}
	return ""
}

// photoSize returns the canvas size of data, which must be an image in format.
// The standard library has no WebP decoder, so WebP is checked structurally.
func photoSize(data []byte, format string) (width, height int, err error) {
	if format == "webp" {
		if width, height, err = stickerWebPSize(data); err != nil {
			return 0, 0, ErrUnsupportedImage
		}
		return width, height, nil
	}
	config, decoded, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || decoded != format {
		return 0, 0, ErrUnsupportedImage
	}
	return config.Width, config.Height, nil
}

func validateDownloadURL(raw string) error {
	if downloadURLProblem(raw) != "" {
		return ErrUnsafeURL
	}
	return nil
}

// downloadURLProblem names the first rule raw breaks, or returns "" for an
// acceptable media download URL. The name never includes part of the URL.
func downloadURLProblem(raw string) string {
	if raw == "" {
		return "missing"
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return "parse"
	case u.Scheme != "https":
		return "scheme"
	case u.User != nil || u.Port() != "":
		return "authority"
	case u.Path == "":
		return "path"
	}
	host := strings.ToLower(u.Hostname())
	if host != "talk.kakaocdn.net" && !strings.HasSuffix(host, ".talk.kakao.com") {
		return "host"
	}
	return ""
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
		{Key: "c", Value: r.ChatID}, {Key: "t", Value: messagetype.Photo},
		{Key: "s", Value: int64(len(r.Image.Data))}, {Key: "cs", Value: r.Image.Checksum},
		{Key: "e", Value: r.Image.Extension}, {Key: "ex", Value: "{}"},
	})
}

type ShipResponse struct {
	Redirect bool
	Key      string
	Host     string
	Port     int
}

func DecodeShipResponse(body []byte) (ShipResponse, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return ShipResponse{}, invalidResponse(ShipCommand, "bson", body)
	}
	redirect, _, err := fields.Bool(raw, "rd")
	if err != nil {
		return ShipResponse{}, invalidResponse(ShipCommand, "rd_type", body)
	}
	if redirect {
		return ShipResponse{Redirect: true}, nil
	}
	key, ok, err := fields.String(raw, "k")
	if err != nil || !ok || key == "" {
		return ShipResponse{}, invalidResponse(ShipCommand, fieldReason("k", ok, err), body)
	}
	host, ok, err := fields.String(raw, "vh")
	if err != nil || !ok || host == "" {
		return ShipResponse{}, invalidResponse(ShipCommand, fieldReason("vh", ok, err), body)
	}
	port64, ok, err := fields.Int64(raw, "p")
	if err != nil || !ok || port64 <= 0 || port64 > 65535 {
		return ShipResponse{}, invalidResponse(ShipCommand, fieldReason("p", ok, err), body)
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
		{Key: "u", Value: r.UserID}, {Key: "k", Value: r.Key}, {Key: "t", Value: messagetype.Photo},
		{Key: "s", Value: int64(len(r.Image.Data))}, {Key: "c", Value: r.ChatID}, {Key: "mid", Value: r.MediaID},
		{Key: "w", Value: r.Image.Width}, {Key: "h", Value: r.Image.Height},
		{Key: "mm", Value: "99999"}, {Key: "nt", Value: int32(0)}, {Key: "os", Value: "mac"},
		{Key: "av", Value: r.AppVersion}, {Key: "ex", Value: extra}, {Key: "ns", Value: false},
		{Key: "dt", Value: int32(4)}, {Key: "scp", Value: int32(1)},
	})
}

func DecodePostOffset(body []byte, size int) (int, error) {
	raw := bson.Raw(body)
	offset64, ok, err := fields.Int64(raw, "o")
	if err != nil || offset64 < 0 || offset64 > int64(size) {
		return 0, ErrInvalidOffset
	}
	if !ok {
		return 0, nil
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
	logID, ok, err := fields.Int64(result.ChatLog, "logId")
	if err != nil || !ok || logID <= 0 {
		return 0, 0, ErrInvalidResponse
	}
	sendAt, ok, err = fields.Int64(result.ChatLog, "sendAt")
	if err != nil || !ok || sendAt <= 0 {
		return 0, 0, ErrInvalidResponse
	}
	return logID, sendAt, nil
}

func DecodeComplete(body []byte) (SendResult, error) {
	raw := bson.Raw(body)
	value, err := raw.LookupErr("chatLog")
	if err != nil {
		return SendResult{}, invalidResponse(CompleteCommand, "chatLog_missing", body)
	}
	if value.Type != bson.TypeEmbeddedDocument {
		return SendResult{}, invalidResponse(CompleteCommand, "chatLog_type", body)
	}
	chatLog := append(bson.Raw(nil), value.Document()...)
	if err := chatLog.Validate(); err != nil {
		return SendResult{}, invalidResponse(CompleteCommand, "chatLog_bson", body)
	}
	return SendResult{ChatLog: chatLog}, nil
}
