package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxAvatarBytes bounds an individual profile image download.
const MaxAvatarBytes = 4 << 20

var ErrAvatarDownload = errors.New("media: avatar download failed")

// DownloadAvatar retrieves an uncredentialed HTTPS CDN profile image. Errors
// never include the source URL. It validates every redirect and bounds bytes.
func DownloadAvatar(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if ctx == nil || client == nil || ValidateAvatarURL(raw) != nil {
		return nil, ErrAvatarDownload
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, ErrAvatarDownload
	}
	c := *client
	// Profile resources are uncredentialed; never forward the caller's cookie jar.
	c.Jar = nil
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if ValidateAvatarURL(req.URL.String()) != nil || len(via) >= 5 {
			return ErrAvatarDownload
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, ErrAvatarDownload
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || (resp.Request != nil && ValidateAvatarURL(resp.Request.URL.String()) != nil) || resp.ContentLength > MaxAvatarBytes {
		return nil, ErrAvatarDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxAvatarBytes+1))
	if err != nil || len(data) > MaxAvatarBytes {
		return nil, ErrAvatarDownload
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" {
		return nil, ErrAvatarDownload
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return nil, ErrAvatarDownload
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, ErrAvatarDownload
	}
	return data, nil
}

// ValidateAvatarURL restricts avatar resources to the Kakao CDN.
func ValidateAvatarURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Path == "" {
		return ErrAvatarDownload
	}
	host := strings.ToLower(u.Hostname())
	if host != "kakaocdn.net" && !strings.HasSuffix(host, ".kakaocdn.net") {
		return ErrAvatarDownload
	}
	return nil
}
