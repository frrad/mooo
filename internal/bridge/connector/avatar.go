package connector

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"time"

	"github.com/frrad/mooo/internal/protocol/media"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const maxAvatarBytes = media.MaxAvatarBytes

var errAvatarDownload = media.ErrAvatarDownload

func avatarFromURL(raw string) *bridgev2.Avatar {
	if raw == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(raw))
	id := networkid.AvatarID(fmt.Sprintf("url:%x", sum[:]))
	return &bridgev2.Avatar{ID: id, Get: func(ctx context.Context) ([]byte, error) {
		if ctx == nil {
			return nil, errAvatarDownload
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return downloadAvatar(ctx, http.DefaultClient, raw)
	}}
}

func downloadAvatar(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	return media.DownloadAvatar(ctx, client, raw)
}

func validateAvatarURL(raw string) error { return media.ValidateAvatarURL(raw) }
