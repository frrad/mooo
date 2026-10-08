package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type albumMatrixAPI struct {
	bridgev2.MatrixAPI
	uploads [][]byte
	failAt  int
}

func (a *albumMatrixAPI) UploadMedia(_ context.Context, _ id.RoomID, data []byte, _, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	if a.failAt > 0 && len(a.uploads)+1 == a.failAt {
		return "", nil, errors.New("synthetic upload failure")
	}
	a.uploads = append(a.uploads, bytes.Clone(data))
	return "mxc://synthetic/plain", &event.EncryptedFileInfo{URL: "mxc://synthetic/encrypted"}, nil
}
func albumMessage(t *testing.T) (events.MultiPhotoMessage, [][]byte) {
	t.Helper()
	data, err := os.ReadFile("../../../research/fixtures/multiphoto/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment json.RawMessage `json:"attachment"`
		Photos     [][]byte        `json:"photos_base64"`
	}
	if json.Unmarshal(data, &f) != nil {
		t.Fatal("fixture invalid")
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.MultiPhoto}, {Key: "attachment", Value: string(f.Attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.MultiPhotoMessage)
	if !ok {
		t.Fatalf("album not typed: %T", e)
	}
	return m, f.Photos
}
func albumTransport(t *testing.T, photos [][]byte) {
	t.Helper()
	old := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		i := 0
		if req.URL.Path == "/synthetic/imageUrls/1" {
			i = 1
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(photos[i])), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })
}
func TestAlbumEncryptedPartsPreserveOrderAndIdentity(t *testing.T) {
	msg, photos := albumMessage(t)
	albumTransport(t, photos)
	intent := &albumMatrixAPI{}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}
	got, err := convertAlbum(context.Background(), portal, intent, msg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 2 {
		t.Fatal("album parts lost")
	}
	for i, p := range got.Parts {
		metadata := p.DBMetadata.(*KakaoMessageMetadata)
		if p.ID != albumPartID(i) || p.Content.MsgType != event.MsgImage || p.Content.File == nil || p.Content.URL != "" || !bytes.Equal(intent.uploads[i], photos[i]) || metadata.Type != messagetype.MultiPhoto {
			t.Fatal("ordered encrypted album part lost")
		}
	}
}
func TestAlbumResumesMissingPartAfterPartialMatrixSuccess(t *testing.T) {
	msg, photos := albumMessage(t)
	albumTransport(t, photos)
	kc, _ := newTestClient(t, nil)
	remote := kc.albumEvent(msg)
	if remote.GetType() != bridgev2.RemoteEventMessageUpsert {
		t.Fatal("album must resume, not discard any existing part as a duplicate")
	}
	res, err := remote.HandleExisting(context.Background(), nil, nil, []*database.Message{{PartID: albumPartID(0)}})
	if err != nil || !res.ContinueMessageHandling {
		t.Fatal("partial album incorrectly treated as complete")
	}
	intent := &albumMatrixAPI{}
	got, err := remote.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, intent)
	if err != nil || len(got.Parts) != 1 || got.Parts[0].ID != albumPartID(1) || !bytes.Equal(intent.uploads[0], photos[1]) {
		t.Fatal("completed first part repeated or second part lost")
	}
	res, err = remote.HandleExisting(context.Background(), nil, nil, []*database.Message{{PartID: albumPartID(0)}, {PartID: albumPartID(1)}})
	if err != nil || res.ContinueMessageHandling {
		t.Fatal("complete album replayed")
	}
}
func TestAlbumTransientUploadFailureLeavesCursorUncommitted(t *testing.T) {
	msg, photos := albumMessage(t)
	albumTransport(t, photos)
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		_, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, &albumMatrixAPI{failAt: 2})
		if !errors.Is(err, errPhotoTransfer) {
			t.Fatal("upload failure not transient")
		}
		return bridgev2.EventHandlingResultFailed.WithError(err)
	}
	if kc.handleEvent(fake, msg) || len(fake.committed()) != 0 {
		t.Fatal("failed album committed cursor")
	}
}

func TestAlbumDeterministicGapKeepsOtherPhoto(t *testing.T) {
	msg, photos := albumMessage(t)
	albumTransport(t, photos)
	msg.Message.Photos[0].ExpiresAt = 1
	msg.Message.Comments[1] = "synthetic caption"
	intent := &albumMatrixAPI{}
	got, err := convertAlbum(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, intent, msg, nil)
	if err != nil || len(got.Parts) != 2 || got.Parts[0].Content.MsgType != event.MsgNotice || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).ConversionGap != "expired" || got.Parts[1].Content.MsgType != event.MsgImage || got.Parts[1].Content.Body != "synthetic caption" || !bytes.Equal(intent.uploads[0], photos[1]) {
		t.Fatal("one deterministic gap lost the rest of the album or its caption")
	}
}
