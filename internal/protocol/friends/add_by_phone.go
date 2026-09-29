// Package friends contains the authenticated friend/contact protocol.
package friends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	BaseURL        = "https://katalk.kakao.com"
	AddByPhonePath = "/mac/friends/add_by_phonenumber.json"
	maxResponse    = 1 << 20
)

var (
	ErrInvalidRequest  = errors.New("friends: invalid add-by-phone request")
	ErrInvalidResponse = errors.New("friends: invalid add-by-phone response")
	ErrRejected        = errors.New("friends: add-by-phone rejected")
)

type ClientProfile struct {
	AppVersion  string
	OSVersion   string
	Language    string
	AccessToken string
	DeviceUUID  string
}

type AddByPhoneRequest struct {
	PhoneNumber string
	CountryISO  string
	CountryCode string
	NickName    string
	Referrer    string
}

func (r AddByPhoneRequest) values() (url.Values, error) {
	if strings.TrimSpace(r.PhoneNumber) == "" || strings.TrimSpace(r.CountryISO) == "" || strings.TrimSpace(r.CountryCode) == "" {
		return nil, ErrInvalidRequest
	}
	for _, value := range []string{r.PhoneNumber, r.CountryISO, r.CountryCode, r.NickName, r.Referrer} {
		if strings.IndexByte(value, 0) >= 0 {
			return nil, ErrInvalidRequest
		}
	}
	referrer := r.Referrer
	if referrer == "" {
		referrer = "etc"
	}
	v := url.Values{
		"phonenumber":  {r.PhoneNumber},
		"country_iso":  {strings.ToUpper(r.CountryISO)},
		"country_code": {r.CountryCode},
		"nickname":     {r.NickName},
		"referrer":     {referrer},
	}
	return v, nil
}

func (p ClientProfile) validate() error {
	for _, value := range []string{p.AppVersion, p.OSVersion, p.Language, p.AccessToken, p.DeviceUUID} {
		if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 {
			return ErrInvalidRequest
		}
	}
	return nil
}

// NewAddByPhoneHTTPRequest reproduces the current Mac authenticated WAS request.
// Authorization is accessToken-hashedDeviceUUID; neither value is safe to log.
func NewAddByPhoneHTTPRequest(ctx context.Context, profile ClientProfile, add AddByPhoneRequest) (*http.Request, error) {
	if ctx == nil || profile.validate() != nil {
		return nil, ErrInvalidRequest
	}
	values, err := add.values()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+AddByPhonePath, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("Accept-Language", profile.Language)
	req.Header.Set("User-Agent", fmt.Sprintf("KT/%s Mc/%s %s", profile.AppVersion, profile.OSVersion, profile.Language))
	req.Header.Set("A", fmt.Sprintf("mac/%s/%s", profile.AppVersion, profile.Language))
	req.Header.Set("Authorization", profile.AccessToken+"-"+profile.DeviceUUID)
	return req, nil
}

type Friend struct {
	UserID int64 `json:"userId"`
}

type AddByPhoneResponse struct {
	Status int32  `json:"status"`
	Friend Friend `json:"friend"`
}

func DecodeAddByPhoneResponse(body []byte) (AddByPhoneResponse, error) {
	var response AddByPhoneResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return AddByPhoneResponse{}, ErrInvalidResponse
	}
	if response.Status != 0 {
		return response, fmt.Errorf("%w: status %d", ErrRejected, response.Status)
	}
	if response.Friend.UserID <= 0 {
		return AddByPhoneResponse{}, ErrInvalidResponse
	}
	return response, nil
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// AddByPhone performs one request and never retries an ambiguous mutation.
func AddByPhone(ctx context.Context, doer Doer, profile ClientProfile, add AddByPhoneRequest) (AddByPhoneResponse, error) {
	if doer == nil {
		return AddByPhoneResponse{}, ErrInvalidRequest
	}
	req, err := NewAddByPhoneHTTPRequest(ctx, profile, add)
	if err != nil {
		return AddByPhoneResponse{}, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return AddByPhoneResponse{}, fmt.Errorf("friends: add-by-phone transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return AddByPhoneResponse{}, ErrInvalidResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AddByPhoneResponse{}, fmt.Errorf("%w: http %d", ErrRejected, resp.StatusCode)
	}
	return DecodeAddByPhoneResponse(body)
}
