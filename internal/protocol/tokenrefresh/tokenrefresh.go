// Package tokenrefresh implements the reviewed Mac access-token renewal request.
package tokenrefresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/macweb"
)

const (
	BaseURL = "https://katalk.kakao.com"
	Path    = "/mac/account/renew_token.json"
	maxBody = 1 << 20
)

var (
	ErrInvalidRequest  = errors.New("tokenrefresh: invalid request")
	ErrInvalidResponse = errors.New("tokenrefresh: invalid response")
	ErrRejected        = errors.New("tokenrefresh: rejected")
)

type ClientProfile = macweb.Profile

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
	if ctx == nil || profile.Validate(true) != nil || !validSecret(request.RefreshToken) {
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
	macweb.ApplyHeaders(req, profile, true)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	return req, nil
}

// Execute sends one renewal request. Any 2xx status is decoded; a non-2xx
// status returns a *macweb.StatusError and a transport failure wraps
// macweb.ErrTransport.
func Execute(ctx context.Context, doer macweb.Doer, profile ClientProfile, request Request) (Rotation, error) {
	if doer == nil {
		return Rotation{}, ErrInvalidRequest
	}
	req, err := NewHTTPRequest(ctx, profile, request)
	if err != nil {
		return Rotation{}, err
	}
	body, err := macweb.Do(doer, req, maxBody)
	if errors.Is(err, macweb.ErrResponseTooLarge) || errors.Is(err, macweb.ErrInvalidResponse) {
		return Rotation{}, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	if err != nil {
		return Rotation{}, err
	}
	return DecodeResponse(body)
}

func DecodeResponse(body []byte) (Rotation, error) {
	var object map[string]json.RawMessage
	if err := macweb.DecodeJSONObject(body, &object); err != nil {
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

func validSecret(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len(value) <= 16384
}
