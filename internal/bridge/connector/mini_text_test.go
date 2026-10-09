package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	bridgedb "maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestMiniBudgetBecomesNoticeBeforeUploads(t *testing.T) {
	data := append(connectorPNG(t), make([]byte, 3<<20)...)
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
	msg := events.MiniTextMessage{TextMessage: events.TextMessage{ChatID: 42, LogID: 99, Message: "(one)(two)(three)"}, Parts: []events.MiniTextPart{{Text: "(one)", ResourceID: "1200000000_1"}, {Text: "(two)", ResourceID: "1200000000_2"}, {Text: "(three)", ResourceID: "1200000000_3"}}}
	intent := &photoMatrixAPI{}
	portal := &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}
	got, err := convertMiniText(context.Background(), portal, intent, msg, nil)
	if err != nil {
		t.Fatalf("deterministic resource budget blocked delivery: %v", err)
	}
	if len(got.Parts) != 3 || len(intent.uploaded) != 0 {
		t.Fatal("budget failure uploaded media or lost parts")
	}
	for _, p := range got.Parts {
		if p.Content.MsgType != event.MsgNotice {
			t.Fatal("missing budget notice")
		}
	}
}

func observedMiniMessage(t *testing.T, index int) events.MiniTextMessage {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/mini-emoticons/observed-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Message    string
			Attachment json.RawMessage
		}
	}
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	c := fixture.Cases[index]
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Text}, {Key: "message", Value: c.Message}, {Key: "attachment", Value: string(c.Attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.MiniTextMessage)
	if !ok {
		t.Fatalf("Mini not typed: %T", e)
	}
	return m
}

func miniTransport(t *testing.T, status int) []byte {
	t.Helper()
	data := connectorPNG(t)
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://item.kakaocdn.net/dw/1200000000.emoji_001.png" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("Mini request escaped resource contract")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
	return data
}

func TestObservedMiniPartsKeepSourceAndEncryptedMedia(t *testing.T) {
	for _, index := range []int{0, 1, 2, 3} {
		t.Run([]string{"pure newline", "mixed newline", "mixed direct", "pure direct"}[index], func(t *testing.T) {
			msg := observedMiniMessage(t, index)
			data := miniTransport(t, 200)
			kc, _ := newTestClient(t, nil)
			remote := kc.miniTextEvent(msg)
			intent := &albumMatrixAPI{}
			got, err := remote.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, intent)
			if err != nil || len(got.Parts) != len(msg.Parts) || len(intent.uploads) != 1 || !bytes.Equal(intent.uploads[0], data) {
				t.Fatal("Mini resource or parts lost")
			}
			var source strings.Builder
			for i, p := range got.Parts {
				source.WriteString(p.Content.Body)
				if p.ID != miniTextPartID(i) || p.DBMetadata.(*KakaoMessageMetadata).Preview != msg.Message {
					t.Fatal("part identity or source preview lost")
				}
				if msg.Parts[i].ResourceID != "" {
					if p.Content.MsgType != event.MsgImage || p.Content.URL != "" || p.Content.File == nil || p.Content.Info.MimeType != "image/png" || p.Content.Info.Width != 2 {
						t.Fatal("native encrypted Mini image lost")
					}
				} else if p.Content.MsgType != event.MsgText {
					t.Fatal("surrounding text changed type")
				}
			}
			if source.String() != msg.Message {
				t.Fatal("ordered fallback text changed")
			}
		})
	}
}

func TestMiniResumesOnlyMissingParts(t *testing.T) {
	msg := observedMiniMessage(t, 1)
	miniTransport(t, 200)
	kc, _ := newTestClient(t, nil)
	remote := kc.miniTextEvent(msg)
	res, err := remote.HandleExisting(context.Background(), nil, nil, []*bridgedb.Message{{PartID: miniTextPartID(0)}})
	if err != nil || !res.ContinueMessageHandling {
		t.Fatal("partial Mini treated as complete")
	}
	got, err := remote.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, &albumMatrixAPI{})
	if err != nil || len(got.Parts) != 2 || got.Parts[0].ID != miniTextPartID(1) || got.Parts[1].ID != miniTextPartID(2) {
		t.Fatal("missing parts lost or completed text repeated")
	}
	res, err = remote.HandleExisting(context.Background(), nil, nil, []*bridgedb.Message{{PartID: miniTextPartID(0)}, {PartID: miniTextPartID(1)}, {PartID: miniTextPartID(2)}})
	if err != nil || res.ContinueMessageHandling {
		t.Fatal("complete Mini replayed")
	}
}

func TestMiniFailureCursorPolicy(t *testing.T) {
	for _, status := range []int{404, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			msg := observedMiniMessage(t, 1)
			miniTransport(t, status)
			kc, _ := newTestClient(t, nil)
			fake := &fakeKakao{}
			kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
				got, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, &albumMatrixAPI{})
				if status == 404 {
					if err != nil || len(got.Parts) != 3 || got.Parts[1].Content.MsgType != event.MsgNotice || got.Parts[0].Content.Body != msg.Parts[0].Text || got.Parts[2].Content.Body != msg.Parts[2].Text {
						t.Fatal("notice lost surrounding source")
					}
					return bridgev2.EventHandlingResultSuccess
				}
				if !errors.Is(err, errMiniTransfer) {
					t.Fatal("transient failure not sanitized")
				}
				return bridgev2.EventHandlingResultFailed.WithError(err)
			}
			handled := kc.handleEvent(fake, msg)
			if status == 404 {
				if !handled || len(fake.committed()) != 1 {
					t.Fatal("deterministic gap did not commit")
				}
			} else if handled || len(fake.committed()) != 0 {
				t.Fatal("transient failure committed")
			}
		})
	}
}
