package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/macweb"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const WriteMediaCommand = "WRITEMEDIA"
const photoPrepareURL = "https://talk-pilsner.kakao.com/messaging/files/v1/prepare"

// PhotoTicket holds private, ephemeral upload authorization. Never log its fields.
type PhotoTicket struct {
	URL          string `json:"url"`
	Resource     string `json:"resource"`
	ResourceType string `json:"resourceType"`
	UploadKey    string `json:"uploadKey"`
	ChunkSize    int64  `json:"chunkSize"`
	NeedReady    bool   `json:"needReady"`
}

func (PhotoTicket) String() string     { return "PhotoTicket{<redacted>}" }
func (t PhotoTicket) GoString() string { return t.String() }

// PrepareDirectPhoto reserves one HTTP upload after a successful SHIP redirect.
// Tenth and processing-readiness strategies remain unsupported and fail before
// uploading bytes or attempting a chat write.
func PrepareDirectPhoto(ctx context.Context, doer macweb.Doer, p macweb.Profile, chatID int64, image Image) (PhotoTicket, error) {
	if ctx == nil || doer == nil || chatID <= 0 || p.UserID <= 0 {
		return PhotoTicket{}, ErrInvalidImage
	}
	if err := p.Validate(true); err != nil {
		return PhotoTicket{}, err
	}
	if len(image.Data) == 0 || len(image.Data) > MaxImageBytes || (image.Extension != "jpg" && image.Extension != "png") {
		return PhotoTicket{}, ErrInvalidImage
	}
	body, err := json.Marshal(struct {
		ChatID    int64  `json:"chatId"`
		MediaType string `json:"mediaType"`
		Size      int    `json:"size"`
		Extension string `json:"extension"`
	}{chatID, "image", len(image.Data), image.Extension})
	if err != nil {
		return PhotoTicket{}, ErrInvalidImage
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, photoPrepareURL, bytes.NewReader(body))
	if err != nil {
		return PhotoTicket{}, ErrInvalidImage
	}
	macweb.ApplyHeaders(req, p, true)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("talk-agent", "macos/"+p.AppVersion)
	req.Header.Set("talk-user-id", strconv.FormatInt(p.UserID, 10))
	req.Header.Set("talk-language", p.Language)
	raw, err := directPhotoHTTP(ctx, photoDoer(doer, false), req)
	if err != nil {
		return PhotoTicket{}, err
	}
	var ticket PhotoTicket
	if macweb.DecodeJSONObject(raw, &ticket) != nil {
		return PhotoTicket{}, ErrInvalidResponse
	}
	if ticket.ResourceType != "vk" || ticket.NeedReady || ticket.ChunkSize <= 0 {
		return PhotoTicket{}, ErrUploadRedirect
	}
	if ticket.ChunkSize > MaxImageBytes || !safePhotoValue(ticket.UploadKey) || !safePhotoValue(ticket.Resource) {
		return PhotoTicket{}, ErrInvalidResponse
	}
	if _, err := ticket.filename(); err != nil {
		return PhotoTicket{}, err
	}
	if _, err := ticket.uploadURL(); err != nil {
		return PhotoTicket{}, err
	}
	return ticket, nil
}

func safePhotoValue(s string) bool {
	return s != "" && len(s) <= 16384 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func (t PhotoTicket) filename() (string, error) {
	u, err := url.Parse(t.Resource)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrInvalidResponse
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || !safePhotoValue(name) || strings.ContainsAny(name, "\\\"") {
		return "", ErrInvalidResponse
	}
	return name, nil
}
func (t PhotoTicket) uploadURL() (*url.URL, error) {
	u, err := url.Parse(t.URL)
	if err != nil || u.Scheme != "https" || u.Host != "kage.talk.kakao.com" || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/offset-up/") {
		return nil, ErrUnsafeURL
	}
	return u, nil
}

// UploadDirectPhoto sends each chunk once. The filename MUST come from the
// prepared resource, not the local filename: a different filename produces
// the server's misleading "Not found upload ready key" response.
func UploadDirectPhoto(ctx context.Context, doer macweb.Doer, p macweb.Profile, t PhotoTicket, image Image) error {
	if ctx == nil || doer == nil || len(image.Data) == 0 || len(image.Data) > MaxImageBytes || t.ResourceType != "vk" || t.NeedReady || t.ChunkSize <= 0 || t.ChunkSize > MaxImageBytes || (image.Extension != "jpg" && image.Extension != "png") {
		return ErrInvalidImage
	}
	if err := p.Validate(false); err != nil {
		return err
	}
	name, err := t.filename()
	if err != nil {
		return err
	}
	u, err := t.uploadURL()
	if err != nil {
		return err
	}
	for start := 0; start < len(image.Data); {
		end := min(start+int(t.ChunkSize), len(image.Data))
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("offset", strconv.Itoa(start)); err != nil {
			return ErrInvalidImage
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file_1"; filename="%s"`, name))
		mimeType := "image/jpeg"
		if image.Extension == "png" {
			mimeType = "image/png"
		}
		h.Set("Content-Type", mimeType)
		part, err := writer.CreatePart(h)
		if err != nil {
			return ErrInvalidImage
		}
		if _, err = part.Write(image.Data[start:end]); err != nil {
			return ErrInvalidImage
		}
		if err = writer.Close(); err != nil {
			return ErrInvalidImage
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body.Bytes()))
		if err != nil {
			return ErrInvalidImage
		}
		macweb.ApplyHeaders(req, p, false)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		raw, err := directPhotoHTTP(ctx, photoDoer(doer, true), req)
		if err != nil {
			return err
		}
		if err = decodePhotoUpload(raw, name, int64(len(image.Data)), end, end == len(image.Data)); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// Reuse the caller's transport without following redirects. Signed uploads
// additionally omit the account cookie jar. Custom Doers retain their seam.
func photoDoer(d macweb.Doer, upload bool) macweb.Doer {
	if c, ok := d.(*http.Client); ok {
		copy := *c
		if upload {
			copy.Jar = nil
		}
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return &copy
	}
	return d
}

func directPhotoHTTP(ctx context.Context, d macweb.Doer, req *http.Request) ([]byte, error) {
	resp, err := d.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, macweb.ErrTransport
	}
	if resp == nil || resp.Body == nil {
		return nil, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrInvalidResponse
	}
	if len(raw) > 1<<20 {
		return nil, macweb.ErrResponseTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &macweb.StatusError{Code: resp.StatusCode}
	}
	return raw, nil
}

func decodePhotoUpload(raw []byte, name string, size int64, next int, final bool) error {
	var response struct {
		Offset  string `json:"offset"`
		Message string `json:"msg"`
		Info    []struct {
			Filename    string `json:"filename"`
			Length      int64  `json:"length"`
			ContentType string `json:"content_type"`
		} `json:"info"`
	}
	if macweb.DecodeJSONObject(raw, &response) != nil {
		return ErrInvalidResponse
	}
	if !final {
		offset, err := strconv.ParseInt(response.Offset, 10, 64)
		if err != nil || offset != int64(next) {
			return ErrInvalidOffset
		}
		return nil
	}
	if response.Message != "ok" || len(response.Info) != 1 || response.Info[0].Filename != name || response.Info[0].Length != size || response.Info[0].ContentType != "image" {
		return ErrInvalidResponse
	}
	return nil
}

// DirectPhotoWriteRequest matches the Mac WRITEMEDIA photo serializer. Resource
// metadata is a nested BSON object; the caption remains the JSON extra string.
type DirectPhotoWriteRequest struct {
	ChatID, MessageID int64
	Image             Image
	Ticket            PhotoTicket
	Caption           string
}

func (r DirectPhotoWriteRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || r.MessageID < 0 || r.Image.Width <= 0 || r.Image.Height <= 0 || !safePhotoValue(r.Ticket.Resource) || !safePhotoValue(r.Ticket.UploadKey) || r.Ticket.ResourceType != "vk" {
		return nil, ErrInvalidImage
	}
	if !ValidCaption(r.Caption) {
		return nil, ErrInvalidCaption
	}
	doc := bson.D{{Key: "chatId", Value: r.ChatID}, {Key: "msgId", Value: r.MessageID}, {Key: "scope", Value: int32(1)}, {Key: "type", Value: int32(2)}, {Key: "noSeen", Value: false}, {Key: "noLight", Value: false}, {Key: "silence", Value: false}, {Key: "photo", Value: bson.D{{Key: "resource", Value: r.Ticket.Resource}, {Key: "resourceType", Value: r.Ticket.ResourceType}, {Key: "hfac", Value: r.Ticket.UploadKey}, {Key: "width", Value: r.Image.Width}, {Key: "height", Value: r.Image.Height}}}}
	if r.Caption != "" {
		extra, _ := json.Marshal(map[string]string{"cmt": r.Caption})
		doc = append(doc, bson.E{Key: "extra", Value: string(extra)})
	}
	return bson.Marshal(doc)
}

func DecodeDirectPhotoWrite(body []byte, chatID int64) (SendResult, error) {
	raw := bson.Raw(body)
	id, present, err := fields.Int64(raw, "chatId")
	if err != nil || !present || id != chatID {
		return SendResult{}, invalidResponse(WriteMediaCommand, "chatId", body)
	}
	log, ok, err := fields.Document(raw, "chatLog")
	if err != nil {
		return SendResult{}, invalidResponse(WriteMediaCommand, "chatLog_type", body)
	}
	if !ok {
		log = raw
	}
	result := SendResult{ChatLog: bytes.Clone(log)}
	if _, _, err = SendResultPosition(result); err != nil {
		return SendResult{}, invalidResponse(WriteMediaCommand, "position", body)
	}
	return result, nil
}
