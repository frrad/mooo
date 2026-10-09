package client

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/frrad/mooo/internal/protocol/media"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type profilePhotoTransport func(*http.Request) (*http.Response, error)

func (f profilePhotoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestContactPhotoResolvesSelectedProfileAndDownloadsWithoutCredentials(t *testing.T) {
	data := syntheticClientJPEG(t)
	backend := newScriptedBackend(t, false, expectRequest("MEMBER", func(raw bson.Raw) error {
		if err := requireInt64(raw, "chatId", 42); err != nil {
			return err
		}
		return requireMemberIDs(raw, []int64{7})
	}, statusDocument(bson.E{Key: "chatId", Value: int64(42)}, bson.E{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(7)}, {Key: "pi", Value: "https://talk.kakaocdn.net/avatar"}}}})))
	session := testMetadataSession(backend, 1)
	downloader := &http.Client{Transport: profilePhotoTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://talk.kakaocdn.net/avatar" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect or credentialed CDN request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	c := &Client{session: session}
	got, err := c.ContactProfilePhoto(t.Context(), 42, 7, downloader)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("download bytes=%d error=%v", len(got), err)
	}
	backend.wait(t)
}

func TestContactPhotoRejectsMissingOrMismatchedProfilesBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name       string
		chat, user int64
		photo      string
		want       error
	}{
		{"absent", 42, 7, "", ErrContactPhotoAbsent},
		{"wrong room", 43, 7, "https://talk.kakaocdn.net/avatar", ErrContactProfileUnavailable},
		{"wrong user", 42, 8, "https://talk.kakaocdn.net/avatar", ErrContactProfileUnavailable},
		{"unsafe resource", 42, 7, "https://example.invalid/avatar?secret=private", media.ErrAvatarDownload},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newScriptedBackend(t, false, expectRequest("MEMBER", nil, statusDocument(bson.E{Key: "chatId", Value: tc.chat}, bson.E{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: tc.user}, {Key: "pi", Value: tc.photo}}}})))
			s := testMetadataSession(backend, 1)
			downloader := &http.Client{Transport: profilePhotoTransport(func(*http.Request) (*http.Response, error) {
				t.Error("unexpected CDN request")
				return nil, errors.New("unexpected")
			})}
			if data, err := s.ContactProfilePhoto(t.Context(), 42, 7, downloader); data != nil || !errors.Is(err, tc.want) {
				t.Fatalf("photo=%d err=%v", len(data), err)
			}
			backend.wait(t)
		})
	}
}

func TestContactPhotoRefreshesMetadataAfterFailedCDNDownload(t *testing.T) {
	reply := func(path string) bson.D {
		return statusDocument(bson.E{Key: "chatId", Value: int64(42)}, bson.E{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(7)}, {Key: "pi", Value: "https://talk.kakaocdn.net/" + path}}}})
	}
	backend := newScriptedBackend(t, false, expectRequest("MEMBER", nil, reply("expired")), expectRequest("MEMBER", nil, reply("current")))
	session := testMetadataSession(backend, 1)
	data := syntheticClientJPEG(t)
	var paths []string
	downloader := &http.Client{Transport: profilePhotoTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/expired" {
			return &http.Response{StatusCode: 410, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	if got, err := session.ContactProfilePhoto(t.Context(), 42, 7, downloader); got != nil || !errors.Is(err, media.ErrAvatarDownload) {
		t.Fatalf("expired bytes=%d error=%v", len(got), err)
	}
	if len(paths) != 1 {
		t.Fatal("failed request retried automatically")
	}
	got, err := session.ContactProfilePhoto(t.Context(), 42, 7, downloader)
	if err != nil || !bytes.Equal(got, data) || len(paths) != 2 || paths[1] != "/current" {
		t.Fatalf("fresh profile bytes=%d error=%v paths=%v", len(got), err, paths)
	}
	backend.wait(t)
}
