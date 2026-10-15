package connector

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/client"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

type resourceTestClient struct {
	*fakeKakao
	source *client.Client
}

func (c *resourceTestClient) AcquireHistoryResource(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	return c.source.AcquireHistoryResource(ctx, key, fetch)
}

func (c *resourceTestClient) AcquireLiveMedia(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	return c.source.AcquireLiveMedia(ctx, key, fetch)
}

func TestHistoryPhotoConversionUsesValidatedResourceAfterRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profile")
	store, err := authstate.Create(path, authstate.Config{DeviceName: "synthetic", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InstallCredentials(authstate.Credentials{UserID: testSelfID, AccessToken: "synthetic", AutoLoginMaterial: []byte("synthetic")}); err != nil {
		t.Fatal(err)
	}
	kc, _, _ := newFrameworkTestClient(t, nil)
	photo, data := groupPhotoEvent(t, 110)
	requests := 0
	old := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:test"}}
	for attempt := range 2 {
		source, err := client.OpenWithOptions(path, nil, client.OpenOptions{HistoryPacing: client.HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		kc.client = &resourceTestClient{fakeKakao: &fakeKakao{}, source: source}
		ctx := context.WithValue(t.Context(), historyResourceOwnerKey{}, kc)
		intent := &photoMatrixAPI{}
		converted, convertErr := convertPhoto(ctx, portal, intent, photo)
		shutdownErr := source.Shutdown(t.Context())
		if convertErr != nil || shutdownErr != nil {
			t.Fatal(convertErr, shutdownErr)
		}
		if len(converted.Parts) != 1 || !bytes.Equal(intent.uploaded, data) {
			t.Fatal("lost photo bytes", attempt)
		}
		if requests != 1 {
			t.Fatal("resource refetched after restart", attempt, requests)
		}
	}
}
