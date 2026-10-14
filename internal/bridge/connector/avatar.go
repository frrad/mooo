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

func avatarFromURL(raw string, owners ...*KakaoClient) *bridgev2.Avatar {
	if raw == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(raw))
	id := networkid.AvatarID(fmt.Sprintf("url:%x", sum[:]))
	return &bridgev2.Avatar{ID: id, Get: func(ctx context.Context) ([]byte, error) {
		if ctx == nil {
			return nil, media.ErrAvatarDownload
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if len(owners) > 0 && owners[0] != nil {
			ctx = context.WithValue(ctx, historyResourceOwnerKey{}, owners[0])
		}
		// A profile URL is not necessarily an immutable resource version.
		return historyDependencyBytes(ctx, []any{"avatar", raw}, func(ctx context.Context) ([]byte, error) {
			return media.DownloadAvatar(ctx, historyHTTPClient(http.DefaultClient), raw)
		}, false)
	}}
}
