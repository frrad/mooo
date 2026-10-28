package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
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

// readCarouselFixture returns a carousel fixture's summary and its
// attachment as the wire string, whose VENDOR is itself a JSON string.
func readCarouselFixture(t *testing.T, name string) (summary, attachment string) {
	t.Helper()
	var fixture struct {
		DecodedBody struct {
			ChatLog struct {
				Message    string
				Attachment json.RawMessage
			} `json:"chatLog"`
		} `json:"decoded_body"`
	}
	data, err := os.ReadFile("../../../research/fixtures/leverage/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
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
	return fixture.DecodedBody.ChatLog.Message, string(wire)
}

// The issue's five-card synthetic wire shape must reach the real delivery path.
func TestCarouselIssue434PreservesFiveCards(t *testing.T) {
	summary, attachment := readCarouselFixture(t, "synthetic-carousel.json")
	kc, harness := newTestClient(t, nil)
	_, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, 11, testOtherID, 1700000000, &summary, &attachment))
	if len(converted.Parts) != 6 {
		t.Fatalf("parts = %d, want summary + five captioned cards", len(converted.Parts))
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
	if !strings.Contains(converted.Parts[1].Content.Body, "Check out this week’s sales!") || !strings.Contains(converted.Parts[1].Content.Body, "(KakaoTalk only)") {
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
	if expected, ok := remote.(expectedPartsMessage); !ok || len(expected.ExpectedPartIDs()) != 2 {
		t.Fatal("carousel part set not declared")
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}
	intent := &photoMatrixAPI{}
	converted, err := remote.ConvertMessage(context.Background(), portal, intent)
	if err != nil {
		t.Fatal(err)
	}
	image := converted.Parts[1].Content
	if len(converted.Parts) != 2 || image.FileName != "card.png" || !strings.Contains(image.Body, "Heading") || image.MsgType != event.MsgImage || image.Info.Width != 800 || image.Info.Height != 600 || image.URL != "mxc://example/photo" || !bytes.Equal(intent.uploaded, data) {
		t.Fatal("image transfer lost content")
	}
	res, err := remote.HandleExisting(context.Background(), portal, intent, []*database.Message{{PartID: ""}})
	if err != nil || !res.ContinueMessageHandling {
		t.Fatal("missing image not resumed")
	}
	converted, err = remote.ConvertMessage(context.Background(), portal, intent)
	if err != nil || len(converted.Parts) != 1 || converted.Parts[0].ID != "card-0" {
		t.Fatal("resume did not isolate image")
	}
	res, err = remote.HandleExisting(context.Background(), portal, intent, []*database.Message{{PartID: ""}, {PartID: "card-0"}})
	if err != nil || res.ContinueMessageHandling || calls != 2 {
		t.Fatal("complete carousel retried")
	}
	encrypted, err := convertLeverage(context.Background(), portal, &encryptedPhotoMatrixAPI{plainURL: "mxc://synthetic/plain-photo"}, msg, nil)
	if err != nil || encrypted.Parts[1].Content.URL != "" || encrypted.Parts[1].Content.File == nil {
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
	if err != nil || calls != 1 || len(converted.Parts) != 3 || !strings.Contains(converted.Parts[1].Content.Body, "Image: https://127.0.0.1/private") || !strings.Contains(converted.Parts[2].Content.Body, "Image unavailable") || strings.Contains(converted.Parts[2].Content.Body, "marker") {
		t.Fatal("unsafe fetch or failed image erased card")
	}
}

var updateGolden = flag.Bool("update", false, "rewrite golden files under research/fixtures")

// goldenProvenance marks golden files as mooo rendering policy, chosen in a
// mockup of the observed carousel, not official-client parity.
var goldenProvenance = map[string]string{
	"kind":   "static",
	"client": "mooo-bridge carousel rendering",
	"date":   "2026-10-28",
	"method": "Output of TestObservedCarouselGolden. Rendering chosen in a mockup: one image per card with the card text as caption, bold summary with a dim Ad prefix, dim i/n counter, bold heading, web buttons as linked labels one per line, KakaoTalk-only buttons as dim italic labels, no image tap links",
	"doc":    "research/leverage.md",
}

// goldenPart is the Matrix content of one bridged carousel part.
type goldenPart struct {
	ID            string `json:"id"`
	MsgType       string `json:"msgtype"`
	Body          string `json:"body"`
	FormattedBody string `json:"formatted_body,omitempty"`
	FileName      string `json:"filename,omitempty"`
	Width         int    `json:"w,omitempty"`
	Height        int    `json:"h,omitempty"`
}

// An observed carousel renders exactly as its golden file says. When a new
// carousel looks wrong, add its anonymized fixture here and review the
// golden diff; rewrite goldens with
// go test -tags=goolm ./internal/bridge/connector -run TestObservedCarouselGolden -update
func TestObservedCarouselGolden(t *testing.T) {
	for _, name := range []string{"observed-carousel"} {
		t.Run(name, func(t *testing.T) {
			summary, attachment := readCarouselFixture(t, name+".json")
			evt, err := events.DecodeForDelivery(leverageCardPacket(t, 2004, testOtherID, 1793145600, &summary, &attachment))
			if err != nil {
				t.Fatal(err)
			}
			msg, ok := evt.(events.LeverageMessage)
			if !ok {
				t.Fatalf("decoded %T", evt)
			}
			old := photoHTTPClient
			png := connectorPNG(t)
			photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(png)), Request: req}, nil
			})}
			t.Cleanup(func() { photoHTTPClient = old })
			portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:example.com"}}
			converted, err := convertLeverage(context.Background(), portal, &photoMatrixAPI{}, msg, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got []goldenPart
			for _, part := range converted.Parts {
				c := part.Content
				g := goldenPart{ID: string(part.ID), MsgType: string(c.MsgType), Body: c.Body, FormattedBody: c.FormattedBody, FileName: c.FileName}
				if c.Info != nil {
					g.Width, g.Height = c.Info.Width, c.Info.Height
				}
				got = append(got, g)
			}
			// Unescaped HTML keeps the golden diff readable.
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			golden := struct {
				Provenance any          `json:"provenance"`
				Parts      []goldenPart `json:"parts"`
			}{goldenProvenance, got}
			if err := enc.Encode(golden); err != nil {
				t.Fatal(err)
			}
			out := buf.Bytes()
			path := "../../../research/fixtures/leverage/" + name + ".matrix.json"
			if *updateGolden {
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("rendering differs from %s (rerun with -update and review the diff):\n%s", path, out)
			}
		})
	}
}
