package connector

import (
	"bytes"
	"context"
	"errors"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func seedWholeAlbumMapping(t *testing.T, kc *KakaoClient, p *bridgev2.Portal, msg events.MultiPhotoMessage) {
	t.Helper()
	row := &database.Message{BridgeID: kc.login.Bridge.ID, Room: p.PortalKey, ID: makeMessageID(msg.Message.ChatID, msg.Message.LogID), MXID: "$original_album:test", SenderID: makeUserID(msg.Message.AuthorID), Timestamp: time.Unix(1000, 0), Metadata: newKakaoMessageMetadata(msg.Message.ChatID, msg.Message.LogID, msg.Message.AuthorID, messagetype.MultiPhoto, "[album]", 0)}
	if err := kc.login.Bridge.DB.Message.Insert(t.Context(), row); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRecognizesWholeOutboundAlbumBeforeDependencies(t *testing.T) {
	for _, forward := range []bool{false, true} {
		t.Run(map[bool]string{false: "backward", true: "startup"}[forward], func(t *testing.T) {
			kc, p, mx := pageFramework(t)
			kc.login.Bridge.Matrix = &historyBatchMatrix{groupCreationMatrix: mx}
			msg, _ := albumMessage(t)
			msg.Message.SentAt = 1000
			seedWholeAlbumMapping(t, kc, p, msg)
			page := client.StoredHistoryPage{Complete: true, Events: []events.Event{msg}}
			source := &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 99}}}, pages: []client.StoredHistoryPage{page, page}}
			kc.client = source
			// This source deliberately has no media dependency API. A whole mapping
			// must avoid downloads/uploads, rather than converting duplicate photos.
			got, err := kc.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Forward: forward, Count: 50})
			if err != nil {
				t.Fatalf("complete outbound album invoked dependencies: %v", err)
			}
			if len(got.Messages) != 0 || mx.uploads != 0 {
				t.Fatal("whole album expanded into duplicate parts")
			}
			got.CompleteCallback()
			key := "kakao:history-batch:1000:3000:false"
			if forward {
				key = "kakao:history-batch:1000:3000:true"
			}
			state, found, err := kc.loadHistoryBatch(t.Context(), key)
			if err != nil || !found || state.Pending {
				t.Fatalf("whole mapping did not confirm history: %+v %v", state, err)
			}
			restarted := newKakaoClient(kc.login, kc.store, kc.userID, nil)
			restarted.client = source
			again, err := restarted.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Forward: forward, Count: 50})
			if err != nil || len(again.Messages) != 0 || mx.uploads != 0 {
				t.Fatal("restart redelivered whole album", err)
			}
		})
	}
}

func TestWholeOutboundAlbumEchoConfirmsSourceProgress(t *testing.T) {
	kc, p, mx := pageFramework(t)
	msg, _ := albumMessage(t)
	seedWholeAlbumMapping(t, kc, p, msg)
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise the pinned framework's duplicate/upsert handling.
		return p.Internal().HandleRemoteEvent(t.Context(), kc.login, remote.GetType(), remote)
	}
	backend := &fakeKakao{}
	if !kc.handleEvent(backend, msg) || len(backend.committed()) != 1 {
		t.Fatal("whole outbound album echo paused source progress")
	}
	if len(mx.messageBodies) != 0 || mx.uploads != 0 {
		t.Fatal("whole album echo duplicated Matrix content")
	}
	parts, err := kc.store.messageParts(t.Context(), p.Receiver, makeMessageID(msg.Message.ChatID, msg.Message.LogID))
	if err != nil || len(parts) != 1 || parts[0].MXID != id.EventID("$original_album:test") {
		t.Fatal("original mapping changed", err)
	}
}

// History uses the real SDK dependency cache/admission; only the Matrix batch
// sink and inventory/window are synthetic. This is a recovery regression, not
// evidence for native viewport behavior or an actual historical homeserver.
type albumHistorySource struct {
	*windowSource
	sdk *client.Client
}

func (s *albumHistorySource) AcquireHistoryResource(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	return s.sdk.AcquireHistoryResource(ctx, key, fetch)
}

func TestHistoryAlbumMissingPartReplaysWithoutRepeatingDependencies(t *testing.T) {
	path := provisioningCacheFixture(t)
	kc, p, mx := pageFramework(t)
	p.MXID = "!album:test"
	batch := &historyBatchMatrix{groupCreationMatrix: mx, lost: true}
	kc.login.Bridge.Matrix = batch
	kc.login.Bridge.Config.Backfill.Queue.BatchSize = 50
	msg, photos := albumMessage(t)
	msg.Message.SentAt = 1000
	firstPart := &database.Message{BridgeID: kc.login.Bridge.ID, Room: p.PortalKey, ID: makeMessageID(testChatID, 99), PartID: albumPartID(0), MXID: "$first_photo:test", SenderID: makeUserID(testOtherID), Timestamp: time.Unix(1000, 0), Metadata: newKakaoMessageMetadata(testChatID, 99, testOtherID, messagetype.MultiPhoto, "[album]", 0)}
	if err := kc.login.Bridge.DB.Message.Insert(t.Context(), firstPart); err != nil {
		t.Fatal(err)
	}
	key := "kakao:history-batch:1000:3000:false"
	// Persist the selected window left by an interrupted first-part delivery.
	if err := kc.saveHistoryBatch(t.Context(), key, historyBatch{Version: 1, Through: 99, First: 99, Limit: 50, Pending: true}); err != nil {
		t.Fatal(err)
	}
	downloads := 0
	old := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/synthetic/imageUrls/1" {
			return nil, errors.New("already mapped photo requested")
		}
		downloads++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(photos[1])), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })
	for attempt := range 2 {
		sdk, err := client.OpenWithOptions(path, nil, client.OpenOptions{HistoryPacing: client.HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		source := &albumHistorySource{sdk: sdk, windowSource: &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 99}}}, pages: []client.StoredHistoryPage{{Complete: true, Events: []events.Event{msg}}}}}
		restarted := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), kc.userID, nil)
		restarted.client = source
		kc.login.Client = restarted
		task := &database.BackfillTask{PortalKey: p.PortalKey, UserLoginID: kc.login.ID}
		//nolint:staticcheck // Actual framework fetch, cutoff, batch delivery and confirmation.
		_, sendErr := p.Internal().DoBackwardsBackfill(t.Context(), kc.login, task, nil)
		closeErr := sdk.Shutdown(t.Context())
		if closeErr != nil || (attempt == 0 && sendErr == nil) || (attempt == 1 && sendErr != nil) {
			t.Fatal(sendErr, closeErr)
		}
		if len(source.markReads()) != 0 || len(source.committed()) != 0 {
			t.Fatal("historical album delivery changed source read/live progress")
		}
		if attempt == 1 && (task.Cursor != historyCursor(testChatID, 99) || !task.IsDone) {
			t.Fatal("recovered album did not finish its durable pagination task")
		}
		state, found, err := restarted.loadHistoryBatch(t.Context(), key)
		if err != nil || !found || state.Pending != (attempt == 0) {
			t.Fatal("selection confirmation lost", state, err)
		}
	}
	rows, err := kc.store.messageParts(t.Context(), p.Receiver, makeMessageID(testChatID, 99))
	if err != nil || len(rows) != 2 || downloads != 1 || mx.uploads != 2 || batch.attempts != 2 || len(batch.applied) != 1 {
		t.Fatalf("replay repeated a confirmed part/dependency: rows=%d downloads=%d uploads=%d attempts=%d applied=%d err=%v", len(rows), downloads, mx.uploads, batch.attempts, len(batch.applied), err)
	}
}
