// Package tokenrefresh implements the reviewed Mac access-token renewal request.
package tokenrefresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	BaseURL = "https://katalk.kakao.com"
	Path    = "/mac/account/renew_token.json"
	maxBody = 1 << 20
)

var (
	ErrInvalidRequest       = errors.New("tokenrefresh: invalid request")
	ErrUnexpectedHTTPStatus = errors.New("tokenrefresh: unexpected HTTP status")
	ErrInvalidResponse      = errors.New("tokenrefresh: invalid response")
	ErrRejected             = errors.New("tokenrefresh: rejected")
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type ClientProfile struct {
	AppVersion  string
	OSVersion   string
	Language    string
	AccessToken string
	DeviceUUID  string
}

type Request struct {
	RefreshToken string
}

// Rotation is a complete server-issued replacement token triple.
type Rotation struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
}

func (Rotation) String() string     { return "[redacted token rotation]" }
func (r Rotation) GoString() string { return r.String() }

func NewHTTPRequest(ctx context.Context, profile ClientProfile, request Request) (*http.Request, error) {
	if ctx == nil || !validProfile(profile) || !validSecret(request.RefreshToken) {
		return nil, ErrInvalidRequest
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {request.RefreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+Path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req.Header.Set("A", "mac/"+profile.AppVersion+"/"+profile.Language)
	req.Header.Set("Accept-Language", profile.Language)
	req.Header.Set("User-Agent", "KT/"+profile.AppVersion+" Mc/"+profile.OSVersion+" "+profile.Language)
	req.Header.Set("Authorization", profile.AccessToken+"-"+profile.DeviceUUID)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	return req, nil
}

func Execute(ctx context.Context, doer Doer, profile ClientProfile, request Request) (Rotation, error) {
	if doer == nil {
		return Rotation{}, ErrInvalidRequest
	}
	req, err := NewHTTPRequest(ctx, profile, request)
	if err != nil {
		return Rotation{}, err
	}
	response, err := doer.Do(req)
	if err != nil {
		return Rotation{}, err
	}
	if response == nil || response.Body == nil {
		return Rotation{}, ErrInvalidResponse
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return Rotation{}, ErrInvalidResponse
	}
	if response.StatusCode != http.StatusOK {
		return Rotation{}, fmt.Errorf("%w: %d", ErrUnexpectedHTTPStatus, response.StatusCode)
	}
	return DecodeResponse(body)
}

func DecodeResponse(body []byte) (Rotation, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return Rotation{}, ErrInvalidResponse
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Rotation{}, ErrInvalidResponse
	}
	if raw, ok := object["status"]; ok {
		var status int64
		if err := json.Unmarshal(raw, &status); err != nil {
			return Rotation{}, ErrInvalidResponse
		}
		if status != 0 {
			return Rotation{}, fmt.Errorf("%w: status %d", ErrRejected, status)
		}
	}
	rotation := Rotation{}
	for key, destination := range map[string]*string{
		"access_token": &rotation.AccessToken, "refresh_token": &rotation.RefreshToken,
		"token_type": &rotation.TokenType,
	} {
		raw, ok := object[key]
		if !ok || json.Unmarshal(raw, destination) != nil || !validSecret(*destination) {
			return Rotation{}, ErrInvalidResponse
		}
	}
	return rotation, nil
}

func validProfile(profile ClientProfile) bool {
	for _, value := range []string{profile.AppVersion, profile.OSVersion, profile.Language, profile.AccessToken, profile.DeviceUUID} {
		if !validSecret(value) {
			return false
		}
	}
	return true
}

func validSecret(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len(value) <= 16384
}
