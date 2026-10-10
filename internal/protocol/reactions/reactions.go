// Package reactions implements Kakao's authenticated bubble-reaction HTTP API.
package reactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/frrad/mooo/internal/protocol/macweb"
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
	ErrLookupFailed    = errors.New("reactions: lookup failed")
	// ErrOutcomeUnconfirmed means the server returned a response, but the
	// response does not establish whether the requested state was applied.
	ErrOutcomeUnconfirmed = errors.New("reactions: outcome unconfirmed")
	// ErrOutcomeUnknown covers transport and malformed-response failures where
	// the mutation result cannot be established. Callers must not retry it.
	ErrOutcomeUnknown = errors.New("reactions: outcome unknown")
)

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
func NewHTTPRequest(ctx context.Context, profile macweb.Profile, reaction Request) (*http.Request, error) {
	if ctx == nil || profile.Validate(true) != nil || reaction.validate() != nil {
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
	macweb.ApplyHeaders(req, profile, true)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// NewMembersHTTPRequest builds the current Mac reaction-attribution lookup.
func NewMembersHTTPRequest(ctx context.Context, profile macweb.Profile, chatID, logID int64) (*http.Request, error) {
	if ctx == nil || profile.Validate(true) != nil || chatID <= 0 || logID <= 0 {
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
	macweb.ApplyHeaders(req, profile, true)
	return req, nil
}

type Response struct {
	Status int32 `json:"status"`
}

func DecodeResponse(body []byte) (Response, error) {
	var wire struct {
		Status *int32 `json:"status"`
		Result *bool  `json:"result"`
	}
	if err := macweb.DecodeJSONObject(body, &wire); err != nil {
		return Response{}, ErrInvalidResponse
	}
	if wire.Status == nil && wire.Result == nil {
		return Response{}, ErrInvalidResponse
	}
	response := Response{}
	if wire.Status != nil {
		response.Status = *wire.Status
	}
	if response.Status != 0 {
		return response, fmt.Errorf("%w: status %d", ErrRejected, response.Status)
	}
	if wire.Result != nil && !*wire.Result {
		return response, fmt.Errorf("%w: result false", ErrRejected)
	}
	return response, nil
}

// execute performs one request. A non-2xx status is ErrRejected, a Doer
// failure is macweb.ErrTransport, and a missing, unreadable or oversized body is
// ErrInvalidResponse.
func execute(doer macweb.Doer, req *http.Request) ([]byte, error) {
	body, err := macweb.Do(doer, req, maxResponse)
	switch {
	case err == nil || errors.Is(err, macweb.ErrTransport):
		return body, err
	case errors.Is(err, macweb.ErrStatus):
		return nil, fmt.Errorf("%w: %w", ErrRejected, err)
	default:
		return nil, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
}

// Send performs one reaction mutation and never retries an ambiguous outcome.
func Send(ctx context.Context, doer macweb.Doer, profile macweb.Profile, reaction Request) (Response, error) {
	if doer == nil {
		return Response{}, ErrInvalidRequest
	}
	req, err := NewHTTPRequest(ctx, profile, reaction)
	if err != nil {
		return Response{}, err
	}
	body, err := execute(doer, req)
	if err != nil {
		return Response{}, err
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
	if err := macweb.DecodeJSONObject(body, &fields); err != nil {
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
func FetchMembers(ctx context.Context, doer macweb.Doer, profile macweb.Profile, chatID, logID int64) (MembersResponse, error) {
	if doer == nil {
		return MembersResponse{}, ErrInvalidRequest
	}
	req, err := NewMembersHTTPRequest(ctx, profile, chatID, logID)
	if err != nil {
		return MembersResponse{}, err
	}
	body, err := execute(doer, req)
	if err != nil {
		return MembersResponse{}, err
	}
	return DecodeMembersResponse(body)
}
