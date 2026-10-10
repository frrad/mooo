// Package friends contains the authenticated friend/contact protocol.
package friends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/frrad/mooo/internal/protocol/macweb"
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

// NewAddByPhoneHTTPRequest reproduces the current Mac authenticated WAS request.
// Authorization is accessToken-hashedDeviceUUID; neither value is safe to log.
func NewAddByPhoneHTTPRequest(ctx context.Context, profile macweb.Profile, add AddByPhoneRequest) (*http.Request, error) {
	if ctx == nil || profile.Validate(true) != nil {
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
	macweb.ApplyHeaders(req, profile, true)
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

// AddByPhone performs one request and never retries an ambiguous mutation.
func AddByPhone(ctx context.Context, doer macweb.Doer, profile macweb.Profile, add AddByPhoneRequest) (AddByPhoneResponse, error) {
	if doer == nil {
		return AddByPhoneResponse{}, ErrInvalidRequest
	}
	req, err := NewAddByPhoneHTTPRequest(ctx, profile, add)
	if err != nil {
		return AddByPhoneResponse{}, err
	}
	body, err := macweb.Do(doer, req, maxResponse)
	switch {
	case errors.Is(err, macweb.ErrTransport):
		return AddByPhoneResponse{}, err
	case errors.Is(err, macweb.ErrStatus):
		return AddByPhoneResponse{}, fmt.Errorf("%w: %w", ErrRejected, err)
	case err != nil:
		return AddByPhoneResponse{}, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	return DecodeAddByPhoneResponse(body)
}
