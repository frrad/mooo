// Package macweb holds the HTTP conventions shared by the Mac client's
// katalk/talk-pilsner web APIs: the client profile, the compatibility header
// set, one bounded request execution and a strict JSON object decoder.
// Request builders, URLs and response validation stay in the protocol
// packages that own each endpoint.
package macweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
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
}

func (e *StatusError) Error() string {
	return "macweb: unexpected HTTP status " + strconv.Itoa(e.Code)
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
		return nil, &StatusError{Code: resp.StatusCode}
	}
	return body, nil
}

// DecodeJSONObject decodes exactly one JSON object into v and rejects any
// other top-level value or trailing data.
func DecodeJSONObject(body []byte, v any) error {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(v); err != nil {
		return ErrInvalidJSON
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidJSON
	}
	return nil
}
