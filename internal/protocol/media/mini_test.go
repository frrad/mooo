package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMiniPathAllowsOnlyObservedCategory(t *testing.T) {
	path, err := MiniResourcePath("1200000000_001")
	if err != nil || path != "1200000000.emoji_001.png" {
		t.Fatal("observed Mini path changed")
	}
	for _, id := range []string{"", "12_", "12_1_2", "https://example.invalid/1", "12../_1", "１２_1"} {
		if _, err := MiniResourcePath(id); !errors.Is(err, ErrInvalidSticker) {
			t.Fatal("invalid Mini ID accepted")
		}
	}
	if _, err := MiniResourcePath("1400000000_001"); !errors.Is(err, ErrUnsupportedSticker) {
		t.Fatal("animated category inferred without acceptance")
	}
}

func TestMiniResourceCannotRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: stickerTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Host != "item.kakaocdn.net" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("Mini request escaped fixed origin")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://example.invalid/asset"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	if _, err := DownloadMini(context.Background(), client, "1200000000_001"); !errors.Is(err, ErrInvalidStickerResource) || requests != 1 {
		t.Fatal("Mini redirect followed or classified as transient")
	}
}
