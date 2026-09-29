// Package reactions implements Kakao's authenticated bubble-reaction HTTP API.
package reactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	BaseURL     = "https://talk-pilsner.kakao.com"
	basePath    = "/messaging/chats/"
	maxResponse = 1 << 20
)

type Type int32

const (
	Cancel   Type = 0
	Heart    Type = 1
	Like     Type = 2
	Check    Type = 3
	Laugh    Type = 4
	Surprise Type = 5
	Sad      Type = 6
)

var (
	ErrInvalidRequest  = errors.New("reactions: invalid request")
	ErrInvalidResponse = errors.New("reactions: invalid response")
	ErrRejected        = errors.New("reactions: request rejected")
)

type ClientProfile struct {
	AppVersion  string
	OSVersion   string
	Language    string
	AccessToken string
	DeviceUUID  string
}

func (p ClientProfile) validate() error {
	for _, value := range []string{p.AppVersion, p.OSVersion, p.Language, p.AccessToken, p.DeviceUUID} {
		if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 {
			return ErrInvalidRequest
		}
	}
	return nil
}

// Request applies one reaction to one chat log. Type Cancel removes the
// caller's current reaction. LinkID is included only for open-chat messages.
type Request struct {
	ChatID    int64
	LogID     int64
	LinkID    int64
	Type      Type
	RequestID int64
}

func (r Request) validate() error {
	if r.ChatID <= 0 || r.LogID <= 0 || r.LinkID < 0 || r.Type < Cancel || r.Type > Sad || r.RequestID <= 0 {
		return ErrInvalidRequest
	}
	return nil
}

func reactionPath(chatID int64) string {
	return basePath + strconv.FormatInt(chatID, 10) + "/bubble/reactions"
}

func membersPath(chatID, logID int64) string {
	return reactionPath(chatID) + "/" + strconv.FormatInt(logID, 10) + "/members"
}

// NewHTTPRequest builds the exact JSON mutation shape recovered from the
// current Mac client. Authorization values are sensitive and must not be logged.
func NewHTTPRequest(ctx context.Context, profile ClientProfile, reaction Request) (*http.Request, error) {
	if ctx == nil || profile.validate() != nil || reaction.validate() != nil {
		return nil, ErrInvalidRequest
	}
	payload := struct {
		LogID     int64 `json:"logId"`
		Type      Type  `json:"type"`
		RequestID int64 `json:"reqId"`
		LinkID    int64 `json:"linkId,omitempty"`
	}{LogID: reaction.LogID, Type: reaction.Type, RequestID: reaction.RequestID, LinkID: reaction.LinkID}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	requestURL, err := url.JoinPath(BaseURL, reactionPath(reaction.ChatID))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	applyHeaders(req, profile)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// NewMembersHTTPRequest builds the current Mac reaction-attribution lookup.
func NewMembersHTTPRequest(ctx context.Context, profile ClientProfile, chatID, logID int64) (*http.Request, error) {
	if ctx == nil || profile.validate() != nil || chatID <= 0 || logID <= 0 {
		return nil, ErrInvalidRequest
	}
	requestURL, err := url.JoinPath(BaseURL, membersPath(chatID, logID))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	applyHeaders(req, profile)
	return req, nil
}

func applyHeaders(req *http.Request, profile ClientProfile) {
	req.Header.Set("Accept-Language", profile.Language)
	req.Header.Set("User-Agent", fmt.Sprintf("KT/%s Mc/%s %s", profile.AppVersion, profile.OSVersion, profile.Language))
	req.Header.Set("A", fmt.Sprintf("mac/%s/%s", profile.AppVersion, profile.Language))
	req.Header.Set("Authorization", profile.AccessToken+"-"+profile.DeviceUUID)
}

type Response struct {
	Status int32 `json:"status"`
}

func DecodeResponse(body []byte) (Response, error) {
	var wire struct {
		Status *int32 `json:"status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&wire); err != nil {
		return Response{}, ErrInvalidResponse
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Response{}, ErrInvalidResponse
	}
	if wire.Status == nil {
		return Response{}, ErrInvalidResponse
	}
	response := Response{Status: *wire.Status}
	if response.Status != 0 {
		return response, fmt.Errorf("%w: status %d", ErrRejected, response.Status)
	}
	return response, nil
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Send performs one reaction mutation and never retries an ambiguous outcome.
func Send(ctx context.Context, doer Doer, profile ClientProfile, reaction Request) (Response, error) {
	if doer == nil {
		return Response{}, ErrInvalidRequest
	}
	req, err := NewHTTPRequest(ctx, profile, reaction)
	if err != nil {
		return Response{}, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("reactions: transport: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return Response{}, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return Response{}, ErrInvalidResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("%w: http %d", ErrRejected, resp.StatusCode)
	}
	return DecodeResponse(body)
}

type MembersResponse struct {
	Revision int64
	Members  map[Type][]int64
	// Fields preserves the complete response dictionary. The current Mac client
	// forwards this dictionary to its caller and only interprets revision and
	// legacy member buckets itself, so unknown server additions must survive.
	Fields map[string]json.RawMessage
}

func DecodeMembersResponse(body []byte) (MembersResponse, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&fields); err != nil || fields == nil || requireJSONEOF(decoder) != nil {
		return MembersResponse{}, ErrInvalidResponse
	}
	response := MembersResponse{
		Members: make(map[Type][]int64),
		Fields:  make(map[string]json.RawMessage, len(fields)),
	}
	for key, raw := range fields {
		response.Fields[key] = append(json.RawMessage(nil), raw...)
	}
	if raw, ok := fields["revision"]; ok {
		_ = json.Unmarshal(raw, &response.Revision)
	}
	for reactionType := Heart; reactionType <= Sad; reactionType++ {
		raw, ok := fields[strconv.FormatInt(int64(reactionType), 10)]
		if !ok {
			continue
		}
		var members []int64
		if err := json.Unmarshal(raw, &members); err != nil || members == nil {
			continue
		}
		response.Members[reactionType] = members
	}
	return response, nil
}

// NeedsMetaSync reports the cache decision made by the current Mac client after
// a members response: only a positive revision newer than the stored reaction
// metadata revision triggers synchronization.
func (r MembersResponse) NeedsMetaSync(storedRevision int64) bool {
	return r.Revision > 0 && r.Revision > storedRevision
}

// FetchMembers resolves the user IDs behind each current reaction type.
func FetchMembers(ctx context.Context, doer Doer, profile ClientProfile, chatID, logID int64) (MembersResponse, error) {
	if doer == nil {
		return MembersResponse{}, ErrInvalidRequest
	}
	req, err := NewMembersHTTPRequest(ctx, profile, chatID, logID)
	if err != nil {
		return MembersResponse{}, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return MembersResponse{}, fmt.Errorf("reactions: members transport: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return MembersResponse{}, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return MembersResponse{}, ErrInvalidResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MembersResponse{}, fmt.Errorf("%w: http %d", ErrRejected, resp.StatusCode)
	}
	return DecodeMembersResponse(body)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidResponse
	}
	return nil
}
