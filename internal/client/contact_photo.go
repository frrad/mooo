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
	if chatID <= 0 || userID <= 0 || downloadClient == nil {
		return nil, ErrProtocol
	}
	return withSession(c, ctx, func(session *session) ([]byte, error) {
		return session.ContactProfilePhoto(ctx, chatID, userID, downloadClient)
	})
}

func (s *session) ContactProfilePhoto(ctx context.Context, chatID, userID int64, downloadClient *http.Client) ([]byte, error) {
	if s == nil || ctx == nil || chatID <= 0 || userID <= 0 || downloadClient == nil {
		return nil, ErrProtocol
	}
	if userID == s.userID {
		return nil, ErrContactProfileUnavailable
	}
	response, err := call(s, ctx, chatmeta.MemberCommand, chatmeta.MemberRequest{ChatID: chatID, MemberIDs: []int64{userID}}, chatmeta.DecodeMemberResponse)
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
