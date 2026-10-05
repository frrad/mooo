package connector

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
)

func avatarPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestDownloadAvatarValidatesBytesAndRedactsFailures(t *testing.T) {
	data := avatarPNG(t)
	client := &http.Client{Transport: avatarRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	got, err := downloadAvatar(context.Background(), client, "https://talk.kakaocdn.net/avatar?secret=redacted")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("avatar bytes=%d err=%v", len(got), err)
	}
	client = &http.Client{Transport: avatarRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errAvatarDownload })}
	_, err = downloadAvatar(context.Background(), client, "https://talk.kakaocdn.net/avatar?secret=redacted")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked URL: %v", err)
	}
}

func TestDownloadAvatarRejectsRedirectAndOversize(t *testing.T) {
	client := &http.Client{Transport: avatarRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://example.invalid/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	if _, err := downloadAvatar(context.Background(), client, "https://talk.kakaocdn.net/avatar"); err == nil {
		t.Fatal("unsafe redirect accepted")
	}
	client = &http.Client{Transport: avatarRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: maxAvatarBytes + 1, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := downloadAvatar(context.Background(), client, "https://talk.kakaocdn.net/avatar"); err == nil {
		t.Fatal("oversize avatar accepted")
	}
	reader := &countingAvatarReader{Reader: strings.NewReader(strings.Repeat("x", maxAvatarBytes+1))}
	client = &http.Client{Transport: avatarRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(reader)}, nil
	})}
	if _, err := downloadAvatar(context.Background(), client, "https://talk.kakaocdn.net/avatar"); err == nil {
		t.Fatal("unknown-length oversized avatar accepted")
	}
	if reader.n > maxAvatarBytes+1 {
		t.Fatalf("unknown-length response read %d bytes, want at most %d", reader.n, maxAvatarBytes+1)
	}
}

type countingAvatarReader struct {
	io.Reader
	n int
}

func (r *countingAvatarReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += n
	return n, err
}

func TestAvatarIDDoesNotPersistRawURL(t *testing.T) {
	const raw = "https://talk.kakaocdn.net/avatar?secret=private"
	avatar := avatarFromURL(raw)
	if avatar == nil || avatar.ID == "" {
		t.Fatal("avatar source did not produce an ID")
	}
	if strings.Contains(string(avatar.ID), raw) || strings.Contains(string(avatar.ID), "private") {
		t.Fatalf("avatar ID leaked source URL: %q", avatar.ID)
	}
	if validateAvatarURL("https://user:pass@talk.kakaocdn.net/avatar") == nil || validateAvatarURL("https://talk.kakaocdn.net:443/avatar") == nil {
		t.Fatal("avatar URL accepted credentials or explicit port")
	}
	if validateAvatarURL("https://talk.kakao.com/avatar") == nil {
		t.Fatal("non-CDN Kakao host accepted")
	}
}

func TestDownloadAvatarHonorsCanceledContextWithoutLeakingError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: avatarRoundTripper(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	_, err := downloadAvatar(ctx, client, "https://talk.kakaocdn.net/avatar?secret=private")
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "kakaocdn") {
		t.Fatalf("canceled avatar error = %v", err)
	}
}

// The bridgev2 portal owns prior-MXC retention after an avatar failure. This
// connector test only proves that a failed upload remains retryable and that a
// later successful upload returns the new MXC.
func TestAvatarReuploadFailureCanRetry(t *testing.T) {
	data := avatarPNG(t)
	avatar := &bridgev2.Avatar{ID: "avatar-test", Get: func(context.Context) ([]byte, error) { return data, nil }}
	intent := &photoMatrixAPI{uploadErr: errors.New("upload failed")}
	_, hash, err := avatar.Reupload(context.Background(), intent, [32]byte{}, "mxc://previous")
	if err == nil || hash == [32]byte{} {
		t.Fatalf("failed upload result = uri/hash/error %v/%x/%v", "", hash, err)
	}
	intent.uploadErr = nil
	uri, retryHash, err := avatar.Reupload(context.Background(), intent, [32]byte{}, "mxc://previous")
	if err != nil || uri != "mxc://example/photo" || retryHash != hash {
		t.Fatalf("retry result = %q/%x/%v", uri, retryHash, err)
	}
}

type avatarRoundTripper func(*http.Request) (*http.Response, error)

func (f avatarRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
