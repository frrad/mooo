package connector

import (
	"bytes"
	"context"
	"errors"
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

// Audit finding 17: a live inbound photo must not queue behind a paced
// metadata read on the account history gate. Historical conversion of the
// same kind of resource still waits for that gate.
func TestLivePhotoDoesNotWaitBehindPacedMetadataRead(t *testing.T) {
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
	source, err := client.OpenWithOptions(path, nil, client.OpenOptions{HistoryPacing: client.HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	livePhoto, data := groupPhotoEvent(t, 110)
	historicalPhoto, _ := groupPhotoEvent(t, 111)
	historicalPhoto.Message.Attachment.Key = "synthetic-historical"
	old := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })

	// A paced metadata read holds the account gate until released.
	entered, release := make(chan struct{}), make(chan struct{})
	metadataDone := make(chan error, 1)
	go func() {
		_, err := source.AcquireHistoryBytes(t.Context(), "metadata", func(context.Context) ([]byte, error) {
			close(entered)
			<-release
			return []byte("metadata"), nil
		})
		metadataDone <- err
	}()
	<-entered
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if err := <-metadataDone; err != nil {
			t.Error(err)
		}
		if err := source.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})

	kc, _, _ := newFrameworkTestClient(t, nil)
	kc.client = &resourceTestClient{fakeKakao: &fakeKakao{}, source: source}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:test"}}
	owned := context.WithValue(t.Context(), historyResourceOwnerKey{}, kc)

	// Live delivery carries a non-required send state (newMessage).
	liveCtx, cancel := context.WithTimeout(context.WithValue(owned, historyDeliveryKey, &historySendState{}), 2*time.Second)
	defer cancel()
	intent := &photoMatrixAPI{}
	converted, err := convertPhoto(liveCtx, portal, intent, livePhoto)
	if err != nil || converted == nil || len(converted.Parts) != 1 || !bytes.Equal(intent.uploaded, data) {
		t.Fatalf("live photo waited behind the metadata gate: %v", err)
	}

	// Historical delivery (historyMessage) still uses the paced account gate.
	historyCtx, cancelHistory := context.WithTimeout(context.WithValue(owned, historyDeliveryKey, &historySendState{required: true}), 100*time.Millisecond)
	defer cancelHistory()
	historyIntent := &photoMatrixAPI{}
	if _, err = convertPhoto(historyCtx, portal, historyIntent, historicalPhoto); err == nil || historyIntent.uploaded != nil {
		t.Fatal("historical photo bypassed the paced account gate")
	}
	if !errors.Is(historyCtx.Err(), context.DeadlineExceeded) {
		t.Fatal("historical photo failed for a reason other than the paced wait", err)
	}
}
