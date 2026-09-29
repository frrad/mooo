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

const detailsPath = "/emoticon/chat/rx/log-details"

type DetailSource int32

const (
	DetailSourceLegacy DetailSource = 1
	DetailSourceMini   DetailSource = 2
)

type ItemMeta struct {
	ItemCode string
	Name     string
	Title    string
}

type Detail struct {
	Source     DetailSource
	Kind       int64
	ReactionID string
	UserIDs    []int64
	ItemMeta   *ItemMeta
}

type DetailsResponse struct {
	Status  int32
	Details []Detail
	Fields  map[string]json.RawMessage
}

// NewDetailsHTTPRequest builds the separate mini/custom-reaction attribution
// request used by the current Mac client. LinkID is sent only for open chats.
func NewDetailsHTTPRequest(ctx context.Context, profile ClientProfile, chatID, linkID, logID int64) (*http.Request, error) {
	if ctx == nil || profile.validate() != nil || profile.UserID <= 0 || chatID <= 0 || linkID < 0 || logID <= 0 {
		return nil, ErrInvalidRequest
	}
	payload := struct {
		ChatID int64 `json:"chatId"`
		LinkID int64 `json:"linkId,omitempty"`
		LogID  int64 `json:"logId"`
	}{ChatID: chatID, LinkID: linkID, LogID: logID}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	requestURL, err := url.JoinPath(BaseURL, detailsPath)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	applyHeaders(req, profile)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("talk-agent", "macos/"+profile.AppVersion)
	req.Header.Set("talk-user-id", strconv.FormatInt(profile.UserID, 10))
	req.Header.Set("talk-language", profile.Language)
	return req, nil
}

func DecodeDetailsResponse(body []byte) (DetailsResponse, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&fields); err != nil || fields == nil || requireJSONEOF(decoder) != nil {
		return DetailsResponse{}, ErrInvalidResponse
	}
	response := DetailsResponse{Fields: make(map[string]json.RawMessage, len(fields))}
	for key, raw := range fields {
		response.Fields[key] = append(json.RawMessage(nil), raw...)
	}
	var status *int32
	if raw, ok := fields["status"]; ok {
		_ = json.Unmarshal(raw, &status)
	}
	if status == nil {
		return DetailsResponse{}, ErrInvalidResponse
	}
	response.Status = *status
	if response.Status != 0 {
		return response, fmt.Errorf("%w: status %d", ErrRejected, response.Status)
	}
	var wireDetails []struct {
		Kind       *int64          `json:"k"`
		ReactionID *string         `json:"o"`
		UserIDs    []string        `json:"u"`
		ItemMeta   json.RawMessage `json:"itemMeta"`
	}
	if raw, ok := fields["details"]; !ok || json.Unmarshal(raw, &wireDetails) != nil || wireDetails == nil {
		return DetailsResponse{}, ErrInvalidResponse
	}
	response.Details = make([]Detail, 0, len(wireDetails))
	for _, wire := range wireDetails {
		if wire.Kind == nil || *wire.Kind < 0 || wire.ReactionID == nil || *wire.ReactionID == "" || wire.UserIDs == nil {
			continue
		}
		detail := Detail{Source: DetailSourceMini, Kind: *wire.Kind, ReactionID: *wire.ReactionID}
		seen := make(map[int64]struct{}, len(wire.UserIDs))
		for _, rawUserID := range wire.UserIDs {
			userID, err := strconv.ParseInt(rawUserID, 10, 64)
			if err != nil {
				continue
			}
			if _, exists := seen[userID]; exists {
				continue
			}
			seen[userID] = struct{}{}
			detail.UserIDs = append(detail.UserIDs, userID)
		}
		if *wire.Kind == 2 && len(wire.ItemMeta) != 0 && string(wire.ItemMeta) != "null" {
			var meta struct {
				ItemCode *string `json:"itemCode"`
				Name     string  `json:"name"`
				Title    string  `json:"title"`
			}
			if json.Unmarshal(wire.ItemMeta, &meta) == nil && meta.ItemCode != nil && *meta.ItemCode != "" {
				detail.ItemMeta = &ItemMeta{ItemCode: *meta.ItemCode, Name: meta.Name, Title: meta.Title}
			}
		}
		response.Details = append(response.Details, detail)
	}
	return response, nil
}

// FetchDetails resolves mini/custom-reaction attribution independently from
// the legacy /members lookup.
func FetchDetails(ctx context.Context, doer Doer, profile ClientProfile, chatID, linkID, logID int64) (DetailsResponse, error) {
	if doer == nil {
		return DetailsResponse{}, ErrInvalidRequest
	}
	req, err := NewDetailsHTTPRequest(ctx, profile, chatID, linkID, logID)
	if err != nil {
		return DetailsResponse{}, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return DetailsResponse{}, fmt.Errorf("reactions: details transport: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return DetailsResponse{}, ErrInvalidResponse
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return DetailsResponse{}, ErrInvalidResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DetailsResponse{}, fmt.Errorf("%w: http %d", ErrRejected, resp.StatusCode)
	}
	return DecodeDetailsResponse(body)
}

// MergeDetails reproduces the Mac detail model: non-empty legacy buckets are
// ordered first, kind-1 mini entries merge into the matching legacy reaction,
// and remaining mini/custom entries follow in server order.
func MergeDetails(members MembersResponse, mini DetailsResponse) []Detail {
	merged := make([]Detail, 0, len(members.Members)+len(mini.Details))
	byReactionID := make(map[string]int)
	for reactionType := Heart; reactionType <= Sad; reactionType++ {
		userIDs := dedupe(members.Members[reactionType])
		if len(userIDs) == 0 {
			continue
		}
		reactionID := strconv.FormatInt(int64(reactionType), 10)
		byReactionID[reactionID] = len(merged)
		merged = append(merged, Detail{
			Source: DetailSourceLegacy, ReactionID: reactionID, UserIDs: userIDs,
		})
	}
	for _, detail := range mini.Details {
		detail.UserIDs = dedupe(detail.UserIDs)
		if detail.Kind == 1 {
			if index, ok := byReactionID[detail.ReactionID]; ok {
				merged[index].UserIDs = appendUnique(merged[index].UserIDs, detail.UserIDs)
				continue
			}
		}
		merged = append(merged, cloneDetail(detail))
	}
	return merged
}

func dedupe(values []int64) []int64 {
	result := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func appendUnique(existing, additional []int64) []int64 {
	seen := make(map[int64]struct{}, len(existing)+len(additional))
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range additional {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		existing = append(existing, value)
	}
	return existing
}

func cloneDetail(detail Detail) Detail {
	detail.UserIDs = append([]int64(nil), detail.UserIDs...)
	if detail.ItemMeta != nil {
		meta := *detail.ItemMeta
		detail.ItemMeta = &meta
	}
	return detail
}
