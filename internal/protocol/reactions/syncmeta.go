package reactions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// SyncMetaPage is one page of message metadata changes newer than a cursor.
// Items are raw meta objects for the events package to decode.
type SyncMetaPage struct {
	Items []json.RawMessage
	Last  bool
}

// NewSyncMetaHTTPRequest builds the Mac client's reaction metadata resync
// request. The server returned every meta newer than cur regardless of max
// and cnt in owned observation; mooo sends max=cur and cnt=0 because it keeps
// no local meta rows to describe.
func NewSyncMetaHTTPRequest(ctx context.Context, profile ClientProfile, chatID, cur int64) (*http.Request, error) {
	if ctx == nil || profile.validate() != nil || chatID <= 0 || cur <= 0 {
		return nil, ErrInvalidRequest
	}
	requestURL, err := url.JoinPath(BaseURL, "messaging", "chats", strconv.FormatInt(chatID, 10), "chat-log", "meta", "sync-meta")
	if err != nil {
		return nil, ErrInvalidRequest
	}
	query := url.Values{}
	query.Set("cur", strconv.FormatInt(cur, 10))
	query.Set("max", strconv.FormatInt(cur, 10))
	query.Set("cnt", "0")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	applyHeaders(req, profile)
	return req, nil
}

// FetchSyncMeta performs one read-only resync page request.
func FetchSyncMeta(ctx context.Context, doer Doer, profile ClientProfile, chatID, cur int64) (SyncMetaPage, error) {
	if doer == nil {
		return SyncMetaPage{}, ErrInvalidRequest
	}
	req, err := NewSyncMetaHTTPRequest(ctx, profile, chatID, cur)
	if err != nil {
		return SyncMetaPage{}, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return SyncMetaPage{}, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	if resp == nil || resp.Body == nil {
		return SyncMetaPage{}, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return SyncMetaPage{}, ErrInvalidResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SyncMetaPage{}, fmt.Errorf("%w: http %d", ErrRejected, resp.StatusCode)
	}
	var wire struct {
		Content *[]json.RawMessage `json:"content"`
		Last    *bool              `json:"last"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&wire); err != nil || requireJSONEOF(decoder) != nil || wire.Content == nil {
		return SyncMetaPage{}, ErrInvalidResponse
	}
	// The Mac client keeps paging while last is false or absent.
	return SyncMetaPage{Items: *wire.Content, Last: wire.Last != nil && *wire.Last}, nil
}
