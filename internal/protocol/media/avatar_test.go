package media

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestDownloadAvatarValidatesBytesAndRedactsFailures(t *testing.T) {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	got, err := DownloadAvatar(t.Context(), client, "https://talk.kakaocdn.net/avatar?secret=redacted")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("avatar bytes=%d err=%v", len(got), err)
	}
	client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, ErrAvatarDownload })}
	_, err = DownloadAvatar(t.Context(), client, "https://talk.kakaocdn.net/avatar?secret=redacted")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked URL: %v", err)
	}
}

func TestDownloadAvatarRejectsRedirectAndOversize(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://example.invalid/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	if _, err := DownloadAvatar(t.Context(), client, "https://talk.kakaocdn.net/avatar"); err == nil {
		t.Fatal("unsafe redirect accepted")
	}
	client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: MaxAvatarBytes + 1, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := DownloadAvatar(t.Context(), client, "https://talk.kakaocdn.net/avatar"); err == nil {
		t.Fatal("oversize avatar accepted")
	}
	reader := &countingAvatarReader{Reader: strings.NewReader(strings.Repeat("x", MaxAvatarBytes+1))}
	client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(reader)}, nil
	})}
	if _, err := DownloadAvatar(t.Context(), client, "https://talk.kakaocdn.net/avatar"); err == nil {
		t.Fatal("unknown-length oversized avatar accepted")
	}
	if reader.n > MaxAvatarBytes+1 {
		t.Fatalf("unknown-length response read %d bytes, want at most %d", reader.n, MaxAvatarBytes+1)
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

func TestValidateAvatarURLRejectsCredentialsPortsAndNonCDNHosts(t *testing.T) {
	if ValidateAvatarURL("https://user:pass@talk.kakaocdn.net/avatar") == nil || ValidateAvatarURL("https://talk.kakaocdn.net:443/avatar") == nil {
		t.Fatal("avatar URL accepted credentials or explicit port")
	}
	if ValidateAvatarURL("https://talk.kakao.com/avatar") == nil {
		t.Fatal("non-CDN Kakao host accepted")
	}
}

func TestDownloadAvatarHonorsCanceledContextWithoutLeakingError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	_, err := DownloadAvatar(ctx, client, "https://talk.kakaocdn.net/avatar?secret=private")
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "kakaocdn") {
		t.Fatalf("canceled avatar error = %v", err)
	}
}

func TestDownloadAvatarRejectsTruncatedImage(t *testing.T) {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()[:40]
	c := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	if got, err := DownloadAvatar(t.Context(), c, "https://talk.kakaocdn.net/avatar"); err == nil || got != nil {
		t.Fatal("truncated profile image accepted")
	}
}

func TestDownloadAvatarDoesNotForwardClientCookieJar(t *testing.T) {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://talk.kakaocdn.net/avatar")
	jar.SetCookies(u, []*http.Cookie{{Name: "operator-session", Value: "synthetic-secret"}})
	c := &http.Client{Jar: jar, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" {
			t.Error("profile resource received client cookies")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(out.Bytes()))}, nil
	})}
	if _, err := DownloadAvatar(t.Context(), c, u.String()); err != nil {
		t.Fatal(err)
	}
}
