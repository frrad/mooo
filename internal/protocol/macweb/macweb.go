// Package macweb holds the HTTP conventions shared by the Mac client's
// katalk/talk-pilsner web APIs: the client profile, the compatibility header
// set, one bounded request execution and a strict JSON object decoder.
// Request builders, URLs and response validation stay in the protocol
// packages that own each endpoint.
package macweb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/strictjson"
)

// maxProfileValue bounds every profile value carried in a header.
const maxProfileValue = 16384

var (
	ErrInvalidProfile = errors.New("macweb: invalid client profile")
	// ErrTransport wraps a Doer failure. The outcome of a mutation that fails
	// this way is unknown.
	ErrTransport = errors.New("macweb: transport failure")
	// ErrStatus matches every *StatusError.
	ErrStatus           = errors.New("macweb: unexpected HTTP status")
	ErrResponseTooLarge = errors.New("macweb: response body too large")
	// ErrInvalidResponse covers a missing response or an unreadable body.
	ErrInvalidResponse = errors.New("macweb: invalid response")
	ErrInvalidJSON     = errors.New("macweb: invalid JSON object")
)

// StatusError reports a non-2xx HTTP status. errors.Is(err, ErrStatus) holds.
type StatusError struct {
	Code int
	// KakaoStatus, KakaoCode, Reason and DetailCode are the status, code,
	// reason and reason.detailCode fields of a JSON error body, which say why
	// Kakao refused. katalk sends status and an object reason, the
	// talk-pilsner gateway a string reason (research/web-error-bodies.md).
	// Nothing else of the body is kept, since it can carry user-facing text
	// or account details.
	KakaoStatus, KakaoCode, Reason, DetailCode string
}

func (e *StatusError) Error() string {
	msg := "macweb: unexpected HTTP status " + strconv.Itoa(e.Code)
	var fields []string
	for _, f := range [...]struct{ name, value string }{
		{"status", e.KakaoStatus}, {"code", e.KakaoCode},
		{"reason", e.Reason}, {"detailCode", e.DetailCode},
	} {
		if f.value != "" {
			fields = append(fields, f.name+" "+f.value)
		}
	}
	if len(fields) > 0 {
		msg += " (" + strings.Join(fields, ", ") + ")"
	}
	return msg
}

// errorFields fills the Kakao fields of e from a JSON error body. Each is
// kept only if it is a short identifier; free text could carry account data.
func (e *StatusError) errorFields(body []byte) {
	var v struct {
		Status json.RawMessage `json:"status"`
		Code   json.RawMessage `json:"code"`
		Reason json.RawMessage `json:"reason"`
	}
	if json.Unmarshal(body, &v) != nil {
		return
	}
	e.KakaoStatus, e.KakaoCode, e.Reason = identifier(v.Status), identifier(v.Code), identifier(v.Reason)
	var reason struct {
		DetailCode json.RawMessage `json:"detailCode"`
	}
	if json.Unmarshal(v.Reason, &reason) == nil {
		e.DetailCode = identifier(reason.DetailCode)
	}
}

func identifier(raw json.RawMessage) string {
	s := strings.Trim(string(raw), `"`)
	if s == "" || len(s) > 32 {
		return ""
	}
	for _, r := range s {
		plain := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.", r)
		if !plain {
			return ""
		}
	}
	return s
}

func (e *StatusError) Is(target error) bool { return target == ErrStatus }

// Doer executes one HTTP request. *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Profile is the Mac client identity sent with web API requests. AccessToken
// and DeviceUUID form the Authorization header; neither is safe to log.
// UserID is needed only by endpoints that send talk-user-id.
type Profile struct {
	AppVersion  string
	OSVersion   string
	Language    string
	AccessToken string
	DeviceUUID  string
	UserID      int64
}

// Validate checks the values ApplyHeaders writes. The credential fields are
// checked only when requireAuth is set.
func (p Profile) Validate(requireAuth bool) error {
	values := []string{p.AppVersion, p.OSVersion, p.Language}
	if requireAuth {
		values = append(values, p.AccessToken, p.DeviceUUID)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 ||
			!utf8.ValidString(value) || len(value) > maxProfileValue {
			return ErrInvalidProfile
		}
	}
	return nil
}

// ApplyHeaders sets the A, Accept-Language and User-Agent headers the current
// Mac client sends, plus Authorization (accessToken-deviceUUID) when
// authorized is set. It does not validate; call Profile.Validate first.
func ApplyHeaders(req *http.Request, p Profile, authorized bool) {
	req.Header.Set("A", "mac/"+p.AppVersion+"/"+p.Language)
	req.Header.Set("Accept-Language", p.Language)
	req.Header.Set("User-Agent", "KT/"+p.AppVersion+" Mc/"+p.OSVersion+" "+p.Language)
	if authorized {
		req.Header.Set("Authorization", p.AccessToken+"-"+p.DeviceUUID)
	}
}

// Do performs exactly one request and returns at most maxBody bytes of body.
// The body is read and size-checked before the status, so an oversized error
// response reports ErrResponseTooLarge. Any 2xx status is success; other
// statuses return a *StatusError.
func Do(d Doer, req *http.Request, maxBody int64) ([]byte, error) {
	resp, err := d.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	if resp == nil || resp.Body == nil {
		return nil, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, ErrInvalidResponse
	}
	if int64(len(body)) > maxBody {
		return nil, ErrResponseTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := &StatusError{Code: resp.StatusCode}
		statusErr.errorFields(body)
		return nil, statusErr
	}
	return body, nil
}

// DecodeJSONObject decodes exactly one JSON object into v and rejects any
// other top-level value, a duplicate top-level key, or trailing data. Callers
// bound the body size through Do.
func DecodeJSONObject(body []byte, v any) error {
	if strictjson.Unmarshal(body, v, strictjson.Limits{}) != nil {
		return ErrInvalidJSON
	}
	return nil
}
