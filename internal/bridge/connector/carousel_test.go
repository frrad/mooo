package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

// The issue's five-card synthetic wire shape must reach the real delivery path.
func TestCarouselIssue434PreservesFiveCards(t *testing.T) {
	var fixture struct {
		DecodedBody struct {
			ChatLog struct {
				Message    string
				Attachment json.RawMessage
			} `json:"chatLog"`
		} `json:"decoded_body"`
	}
	data, err := os.ReadFile("../../../research/fixtures/leverage/synthetic-carousel.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	summary := fixture.DecodedBody.ChatLog.Message
	var att map[string]any
	if err := json.Unmarshal(fixture.DecodedBody.ChatLog.Attachment, &att); err != nil {
		t.Fatal(err)
	}
	public := att["P"].(map[string]any)
	vendor, err := json.Marshal(public["VENDOR"])
	if err != nil {
		t.Fatal(err)
	}
	public["VENDOR"] = string(vendor)
	wire, err := json.Marshal(att)
	if err != nil {
		t.Fatal(err)
	}
	attachment := string(wire)
	kc, harness := newTestClient(t, nil)
	_, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, 11, testOtherID, 1700000000, &summary, &attachment))
	if len(converted.Parts) != 11 {
		t.Fatalf("parts = %d, want summary + five card texts + five images", len(converted.Parts))
	}
	if !strings.HasSuffix(converted.Parts[0].Content.Body, summary) {
		t.Fatal("summary lost")
	}
	for i, part := range converted.Parts {
		meta := part.DBMetadata.(*KakaoMessageMetadata)
		if meta.Preview != summary || !meta.ReactionUnavailable {
			t.Fatalf("part %d lost summary/restrictions", i)
		}
	}
	if !strings.Contains(converted.Parts[1].Content.Body, "Check out this week’s sales!") || !strings.Contains(converted.Parts[3].Content.Body, "Open in KakaoTalk") {
		t.Fatal("description or app fallback lost")
	}
}

func TestCarouselImageTransferAndResume(t *testing.T) {
	data := connectorPNG(t)
	old := photoHTTPClient
	calls := 0
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host != "talk.kakaocdn.net" {
			t.Fatal("unexpected fetch")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })
	msg := events.LeverageMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Summary: "Summary", ReactionUnavailable: true, Cards: []events.LeverageCard{{Heading: "Heading", Images: []events.LeverageImage{{URL: "https://talk.kakaocdn.net/card.png", Width: 800, Height: 600}}}}}
	kc, _ := newTestClient(t, nil)
	remote := kc.remoteEventFor(msg).(bridgev2.RemoteMessageUpsert)
	if expected, ok := remote.(expectedPartsMessage); !ok || len(expected.ExpectedPartIDs()) != 3 {
		t.Fatal("carousel part set not declared")
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}
	intent := &photoMatrixAPI{}
	converted, err := remote.ConvertMessage(context.Background(), portal, intent)
	if err != nil {
		t.Fatal(err)
	}
	image := converted.Parts[2].Content
	if len(converted.Parts) != 3 || image.MsgType != event.MsgImage || image.Info.Width != 800 || image.Info.Height != 600 || image.URL != "mxc://example/photo" || !bytes.Equal(intent.uploaded, data) {
		t.Fatal("image transfer lost content")
	}
	res, err := remote.HandleExisting(context.Background(), portal, intent, []*database.Message{{PartID: ""}, {PartID: "card-0"}})
	if err != nil || !res.ContinueMessageHandling {
		t.Fatal("missing image not resumed")
	}
	converted, err = remote.ConvertMessage(context.Background(), portal, intent)
	if err != nil || len(converted.Parts) != 1 || converted.Parts[0].ID != "card-0-image-0" {
		t.Fatal("resume did not isolate image")
	}
	res, err = remote.HandleExisting(context.Background(), portal, intent, []*database.Message{{PartID: ""}, {PartID: "card-0"}, {PartID: "card-0-image-0"}})
	if err != nil || res.ContinueMessageHandling || calls != 2 {
		t.Fatal("complete carousel retried")
	}
	encrypted, err := convertLeverage(context.Background(), portal, &encryptedPhotoMatrixAPI{plainURL: "mxc://synthetic/plain-photo"}, msg, nil)
	if err != nil || encrypted.Parts[2].Content.URL != "" || encrypted.Parts[2].Content.File == nil {
		t.Fatal("encrypted image contains plain URL")
	}
	intent.uploadErr = errors.New("private URL marker")
	_, err = convertLeverage(context.Background(), portal, intent, msg, nil)
	if !errors.Is(err, errLeverageTransfer) || strings.Contains(err.Error(), "marker") {
		t.Fatal("unsanitized upload failure")
	}
}

func TestCarouselUnsafeAndFailedImagesKeepReadableFallback(t *testing.T) {
	old := photoHTTPClient
	calls := 0
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("private marker") })}
	t.Cleanup(func() { photoHTTPClient = old })
	msg := events.LeverageMessage{Summary: "summary", Cards: []events.LeverageCard{{Images: []events.LeverageImage{{URL: "https://127.0.0.1/private"}, {URL: "https://talk.kakaocdn.net/expired"}}}}}
	converted, err := convertLeverage(context.Background(), nil, nil, msg, nil)
	if err != nil || calls != 1 || len(converted.Parts) != 4 || !strings.Contains(converted.Parts[3].Content.Body, "Image unavailable") || strings.Contains(converted.Parts[3].Content.Body, "marker") {
		t.Fatal("unsafe fetch or failed image erased card")
	}
}
