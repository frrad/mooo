package media

import (
	"context"
	"net/http"
	"strings"
)

// MiniResourcePath derives only the observed non-animated category-12 path.
// Other Mini categories require independent resource/decoder acceptance.
func MiniResourcePath(resourceID string) (string, error) {
	if len(resourceID) > 64 {
		return "", ErrInvalidSticker
	}
	pack, index, ok := strings.Cut(resourceID, "_")
	if !ok || len(pack) < 2 || index == "" {
		return "", ErrInvalidSticker
	}
	for _, s := range []string{pack, index} {
		for _, c := range s {
			if c < '0' || c > '9' {
				return "", ErrInvalidSticker
			}
		}
	}
	if !strings.HasPrefix(pack, "12") {
		return "", ErrUnsupportedSticker
	}
	return pack + ".emoji_" + index + ".png", nil
}

func DownloadMini(ctx context.Context, client *http.Client, resourceID string) (StickerResource, error) {
	path, err := MiniResourcePath(resourceID)
	if err != nil {
		return StickerResource{}, err
	}
	return DownloadSticker(ctx, client, StickerAttachment{Path: path})
}
