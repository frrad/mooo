package client

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/media"
)

var ErrContactPhotoAbsent = errors.New("client: contact has no profile photo")
var ErrContactProfileUnavailable = errors.New("client: contact profile unavailable")

// ContactProfilePhoto resolves one contact in a selected room and downloads its
// current thumbnail. It does not persist URLs or retry failed requests. Empty
// photos are distinct from missing profiles; self-profile lookup is unsupported.
func (c *Client) ContactProfilePhoto(ctx context.Context, chatID, userID int64, downloadClient *http.Client) ([]byte, error) {
	if c == nil || ctx == nil || chatID <= 0 || userID <= 0 || downloadClient == nil {
		return nil, ErrProtocol
	}
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.ContactProfilePhoto(ctx, chatID, userID, downloadClient)
}

func (s *Session) ContactProfilePhoto(ctx context.Context, chatID, userID int64, downloadClient *http.Client) ([]byte, error) {
	if s == nil || ctx == nil || chatID <= 0 || userID <= 0 || downloadClient == nil {
		return nil, ErrProtocol
	}
	if userID == s.userID {
		return nil, ErrContactProfileUnavailable
	}
	body, err := (chatmeta.MemberRequest{ChatID: chatID, MemberIDs: []int64{userID}}).MarshalBSON()
	if err != nil {
		return nil, err
	}
	reply, err := s.Request(ctx, chatmeta.MemberCommand, body)
	if err != nil {
		return nil, err
	}
	response, err := chatmeta.DecodeMemberResponse(reply.Body)
	if err != nil {
		return nil, err
	}
	if response.ChatID != chatID || len(response.Members) != 1 || response.Members[0].UserID != userID {
		return nil, ErrContactProfileUnavailable
	}
	raw := response.Members[0].ProfileImageURL
	if raw == "" {
		return nil, ErrContactPhotoAbsent
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return media.DownloadAvatar(ctx, downloadClient, raw)
}
