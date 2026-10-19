package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

const (
	WriteMediaCommand = "WRITEMEDIA"
	directPrepareURL  = "https://talk-pilsner.kakao.com/messaging/files/v1/prepare"
	directReadyURL    = "https://talk-pilsner.kakao.com/messaging/files/v1/ready"
)

// ErrUploadFiltered reports media the server's content filter refused while
// preparing it for delivery. No message was created.
var ErrUploadFiltered = errors.New("media: KakaoTalk refused the content of this upload")

// DirectTicket holds private, ephemeral upload authorization. Never log its fields.
type DirectTicket struct {
	URL          string `json:"url"`
	Resource     string `json:"resource"`
	ResourceType string `json:"resourceType"`
	UploadKey    string `json:"uploadKey"`
	ChunkSize    int64  `json:"chunkSize"`
	NeedReady    bool   `json:"needReady"`
}

func (DirectTicket) String() string     { return "DirectTicket{<redacted>}" }
func (t DirectTicket) GoString() string { return t.String() }

// DirectMedia is one payload for the HTTP upload a SHIP redirect selects
// (research/bridge/SHIP-REDIRECT-FILE-VIDEO.md).
type DirectMedia struct {
	// MediaType is the prepare mediaType: "image", "video" or "file".
	MediaType string
	// Extension is the lowercase extension sent to prepare.
	Extension string
	// MIMEType is the Content-Type of each uploaded chunk.
	MIMEType string
	Data     []byte
}

// ImageDirectMedia describes a prepared JPEG or PNG photo.
func ImageDirectMedia(image Image) DirectMedia {
	mimeType := "image/jpeg"
	if image.Extension == "png" {
		mimeType = "image/png"
	}
	return DirectMedia{MediaType: "image", Extension: image.Extension, MIMEType: mimeType, Data: image.Data}
}

// directVideoMIME gives each video extension's MIME type. Files are sent as
// application/octet-stream, which the server accepts for any file; Android
// itself sends only a coarse application/* type.
var directVideoMIME = map[string]string{
	"ts": "video/mp2t", "ogv": "video/ogg", "flv": "video/x-flv", "mov": "video/quicktime", "mp4": "video/mp4",
	"mpeg": "video/mpeg", "mpg": "video/mpeg", "mkv": "video/x-matroska", "wmv": "video/x-ms-wmv",
	"asf": "video/x-ms-asf", "avi": "video/x-msvideo", "m4v": "video/x-m4v",
}

// UploadDirectMedia describes a prepared file or video. A file without an
// extension cannot use the HTTP upload: the official client refuses it
// before prepare, so it returns ErrUploadRedirect.
func UploadDirectMedia(upload Upload) (DirectMedia, error) {
	if !upload.valid() {
		return DirectMedia{}, ErrInvalidUpload
	}
	if upload.Extension == "" {
		return DirectMedia{}, ErrUploadRedirect
	}
	m := DirectMedia{MediaType: "file", Extension: upload.Extension, MIMEType: "application/octet-stream", Data: upload.Data}
	if upload.Type == messagetype.Video {
		m.MediaType = "video"
		if mimeType, ok := directVideoMIME[upload.Extension]; ok {
			m.MIMEType = mimeType
		}
	}
	return m, nil
}

func (m DirectMedia) valid() bool {
	limit := MaxUploadBytes
	switch m.MediaType {
	case "image":
		limit = MaxImageBytes
	case "video", "file":
	default:
		return false
	}
	return len(m.Data) > 0 && len(m.Data) <= limit && safeDirectValue(m.Extension) && safeDirectValue(m.MIMEType)
}

// PrepareDirect reserves one HTTP upload after a successful SHIP redirect.
// Only the Kage ("vk") strategy is supported; Tenth fails with
// ErrUploadRedirect before any bytes are uploaded.
func PrepareDirect(ctx context.Context, doer macweb.Doer, p macweb.Profile, chatID int64, m DirectMedia) (DirectTicket, error) {
	if ctx == nil || doer == nil || chatID <= 0 || p.UserID <= 0 || !m.valid() {
		return DirectTicket{}, ErrInvalidUpload
	}
	if err := p.Validate(true); err != nil {
		return DirectTicket{}, err
	}
	req, err := directJSONRequest(ctx, p, directPrepareURL, struct {
		ChatID    int64  `json:"chatId"`
		MediaType string `json:"mediaType"`
		Size      int    `json:"size"`
		Extension string `json:"extension"`
	}{chatID, m.MediaType, len(m.Data), m.Extension})
	if err != nil {
		return DirectTicket{}, err
	}
	raw, err := directHTTP(ctx, directDoer(doer, false), req)
	if err != nil {
		return DirectTicket{}, err
	}
	var ticket DirectTicket
	if macweb.DecodeJSONObject(raw, &ticket) != nil {
		return DirectTicket{}, ErrInvalidResponse
	}
	if ticket.ResourceType != "vk" || ticket.ChunkSize <= 0 {
		return DirectTicket{}, ErrUploadRedirect
	}
	if ticket.ChunkSize > int64(MaxUploadBytes) || !safeDirectValue(ticket.UploadKey) || !safeDirectValue(ticket.Resource) {
		return DirectTicket{}, ErrInvalidResponse
	}
	if _, err := ticket.filename(); err != nil {
		return DirectTicket{}, err
	}
	if _, err := ticket.uploadURL(); err != nil {
		return DirectTicket{}, err
	}
	return ticket, nil
}

func directJSONRequest(ctx context.Context, p macweb.Profile, endpoint string, body any) (*http.Request, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, ErrInvalidUpload
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, ErrInvalidUpload
	}
	macweb.ApplyHeaders(req, p, true)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("talk-agent", "macos/"+p.AppVersion)
	req.Header.Set("talk-user-id", strconv.FormatInt(p.UserID, 10))
	req.Header.Set("talk-language", p.Language)
	return req, nil
}

func safeDirectValue(s string) bool {
	return s != "" && len(s) <= 16384 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func (t DirectTicket) filename() (string, error) {
	u, err := url.Parse(t.Resource)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrInvalidResponse
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || !safeDirectValue(name) || strings.ContainsAny(name, "\\\"") {
		return "", ErrInvalidResponse
	}
	return name, nil
}

func (t DirectTicket) uploadURL() (*url.URL, error) {
	u, err := url.Parse(t.URL)
	if err != nil || u.Scheme != "https" || u.Host != "kage.talk.kakao.com" || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/offset-up/") {
		return nil, ErrUnsafeURL
	}
	return u, nil
}

// UploadDirect sends each chunk once. The filename MUST come from the
// prepared resource, not the local filename: a different filename produces
// the server's misleading "Not found upload ready key" response.
func UploadDirect(ctx context.Context, doer macweb.Doer, p macweb.Profile, t DirectTicket, m DirectMedia) error {
	if ctx == nil || doer == nil || !m.valid() || t.ResourceType != "vk" || t.ChunkSize <= 0 || t.ChunkSize > int64(MaxUploadBytes) {
		return ErrInvalidUpload
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
	for start := 0; start < len(m.Data); {
		end := min(start+int(t.ChunkSize), len(m.Data))
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("offset", strconv.Itoa(start)); err != nil {
			return ErrInvalidUpload
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file_1"; filename="%s"`, name))
		h.Set("Content-Type", m.MIMEType)
		part, err := writer.CreatePart(h)
		if err != nil {
			return ErrInvalidUpload
		}
		if _, err = part.Write(m.Data[start:end]); err != nil {
			return ErrInvalidUpload
		}
		if err = writer.Close(); err != nil {
			return ErrInvalidUpload
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body.Bytes()))
		if err != nil {
			return ErrInvalidUpload
		}
		macweb.ApplyHeaders(req, p, false)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		raw, err := directHTTP(ctx, directDoer(doer, true), req)
		if err != nil {
			return err
		}
		if err = decodeDirectUpload(raw, m.MediaType, name, int64(len(m.Data)), end, end == len(m.Data)); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// Reuse the caller's transport without following redirects. Signed uploads
// additionally omit the account cookie jar. Custom Doers retain their seam.
func directDoer(d macweb.Doer, upload bool) macweb.Doer {
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

func directHTTP(ctx context.Context, d macweb.Doer, req *http.Request) ([]byte, error) {
	raw, status, err := directHTTPBody(ctx, d, req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &macweb.StatusError{Code: status}
	}
	return raw, nil
}

func directHTTPBody(ctx context.Context, d macweb.Doer, req *http.Request) ([]byte, int, error) {
	resp, err := d.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, macweb.ErrTransport
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, ErrInvalidResponse
	}
	if len(raw) > 1<<20 {
		return nil, 0, macweb.ErrResponseTooLarge
	}
	return raw, resp.StatusCode, nil
}

// decodeDirectUpload checks one Kage acknowledgement. Every intermediate
// chunk must report the end of its range. The final one must say "ok"; a
// photo must also identify the resource, its full length and "image". For a
// file or video the identifying info is checked only when present: video
// returns none, and file content types follow the extension ("etc/dat").
func decodeDirectUpload(raw []byte, mediaType, name string, size int64, next int, final bool) error {
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
	if response.Message != "ok" {
		return ErrInvalidResponse
	}
	if mediaType != "image" && response.Info == nil {
		return nil
	}
	if len(response.Info) != 1 || response.Info[0].Filename != name || response.Info[0].Length != size {
		return ErrInvalidResponse
	}
	if mediaType == "image" && response.Info[0].ContentType != "image" {
		return ErrInvalidResponse
	}
	return nil
}

// DirectReady is one ready-poll reply.
type DirectReady struct {
	Status         int    `json:"status"`
	IntervalMS     int64  `json:"interval"`
	MaxWaitSeconds int64  `json:"maxWaitSeconds"`
	JobID          string `json:"jobId"`
}

// DirectReadyDone is the ready status that allows WRITEMEDIA.
const DirectReadyDone = 1

// CheckDirectReady sends one ready poll for an uploaded resource that
// prepare marked needReady. prevStatus is 0 on the first poll and then the
// last status returned; jobID is the last returned job, if any. The content
// filter's refusal is ErrUploadFiltered.
func CheckDirectReady(ctx context.Context, doer macweb.Doer, p macweb.Profile, chatID int64, mediaType string, t DirectTicket, prevStatus int, jobID string) (DirectReady, error) {
	if ctx == nil || doer == nil || chatID <= 0 || p.UserID <= 0 || !safeDirectValue(t.Resource) || !safeDirectValue(t.UploadKey) || t.ResourceType != "vk" {
		return DirectReady{}, ErrInvalidUpload
	}
	if err := p.Validate(true); err != nil {
		return DirectReady{}, err
	}
	body := map[string]any{
		"resource": t.Resource, "resourceType": t.ResourceType, "chatId": chatID,
		"mediaType": mediaType, "uploadKey": t.UploadKey, "prevStatus": prevStatus,
	}
	if jobID != "" {
		body["jobId"] = jobID
	}
	req, err := directJSONRequest(ctx, p, directReadyURL, body)
	if err != nil {
		return DirectReady{}, err
	}
	raw, status, err := directHTTPBody(ctx, directDoer(doer, false), req)
	if err != nil {
		return DirectReady{}, err
	}
	if status < 200 || status >= 300 {
		var refusal struct {
			Code json.RawMessage `json:"code"`
		}
		if json.Unmarshal(raw, &refusal) == nil && strings.Trim(string(refusal.Code), `"`) == "90025" {
			return DirectReady{}, ErrUploadFiltered
		}
		return DirectReady{}, &macweb.StatusError{Code: status}
	}
	var ready DirectReady
	if macweb.DecodeJSONObject(raw, &ready) != nil || ready.IntervalMS < 0 || ready.MaxWaitSeconds < 0 || (ready.JobID != "" && !safeDirectValue(ready.JobID)) {
		return DirectReady{}, ErrInvalidResponse
	}
	return ready, nil
}
