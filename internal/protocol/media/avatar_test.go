package media

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

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
