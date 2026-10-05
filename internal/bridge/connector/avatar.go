package connector

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const maxAvatarBytes = 4 << 20

var errAvatarDownload = errors.New("connector: avatar download failed")

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
	if ctx == nil || client == nil || validateAvatarURL(raw) != nil {
		return nil, errAvatarDownload
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errAvatarDownload
	}
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if validateAvatarURL(req.URL.String()) != nil || len(via) >= 5 {
			return errAvatarDownload
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, errAvatarDownload
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || (resp.Request != nil && validateAvatarURL(resp.Request.URL.String()) != nil) || resp.ContentLength > maxAvatarBytes {
		return nil, errAvatarDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil || len(data) > maxAvatarBytes {
		return nil, errAvatarDownload
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" {
		return nil, errAvatarDownload
	}
	return data, nil
}

func validateAvatarURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Path == "" {
		return errAvatarDownload
	}
	host := strings.ToLower(u.Hostname())
	if host != "kakaocdn.net" && !strings.HasSuffix(host, ".kakaocdn.net") {
		return errAvatarDownload
	}
	return nil
}
