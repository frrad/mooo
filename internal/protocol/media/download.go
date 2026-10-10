package media

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
)

// downloadResource serves bounded ordinary media. An empty checksum is used
// only for observed audio attachments which do not carry one. Callers
// separately validate their metadata and expiry units before fetching.
func downloadResource(ctx context.Context, client *http.Client, rawURL string, size int64, checksum string) ([]byte, error) {
	data, err := downloadBoundedResource(ctx, client, rawURL, size)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) < size {
		return nil, ErrDownload
	}
	sum := sha1.Sum(data)
	if checksum != "" && !strings.EqualFold(hex.EncodeToString(sum[:]), checksum) {
		return nil, ErrChecksumMismatch
	}
	return data, nil
}

// downloadBoundedResource also handles contacts without an advertised size.
func downloadBoundedResource(ctx context.Context, client *http.Client, rawURL string, size int64) ([]byte, error) {
	if ctx == nil || client == nil || ctx.Err() != nil {
		return nil, ErrDownload
	}
	if size <= 0 || size > 64<<20 {
		return nil, ErrInvalidAttachment
	}
	if validateDownloadURL(rawURL) != nil {
		return nil, ErrUnsafeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrDownload
	}
	c := *client
	previous := client.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if validateDownloadURL(req.URL.String()) != nil {
			return ErrUnsafeURL
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return errors.New("media: too many redirects")
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, ErrUnsafeURL) {
			return nil, ErrUnsafeURL
		}
		return nil, ErrDownload
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && validateDownloadURL(resp.Request.URL.String()) != nil {
		return nil, ErrDownload
	}
	if unavailableStatus(resp.StatusCode) {
		return nil, ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, size+1))
	if err != nil {
		return nil, ErrDownload
	}
	if int64(len(data)) > size {
		return nil, ErrInvalidAttachment
	}
	return data, nil
}
