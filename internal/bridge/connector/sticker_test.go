package connector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"net/http"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"maunium.net/go/mautrix/bridgev2"
	bridgedb "maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestStickerIsConvertedInsteadOfUnsupported(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	remote := kc.remoteEventFor(events.StickerMessage{ChatID: 42, LogID: 99, AuthorID: 8, Type: 12, Attachment: media.StickerAttachment{Path: "synthetic.png"}})
	if remote == nil {
		t.Fatal("sticker must produce remote event")
	}
	data := connectorPNG(t)
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
	intent := &photoMatrixAPI{}
	portal := &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}
	converted, err := convertSticker(context.Background(), portal, intent, events.StickerMessage{ChatID: 42, LogID: 99, AuthorID: 8, Type: 12, Attachment: media.StickerAttachment{Path: "synthetic.png"}})
	if err != nil {
		t.Fatal(err)
	}
	part := converted.Parts[0]
	if part.Type != event.EventSticker || part.Content.Info.MimeType != "image/png" || !bytes.Equal(intent.uploaded, data) {
		t.Fatal("native sticker/asset lost")
	}
}

func TestStickerFailureCursorPolicy(t *testing.T) {
	for _, status := range []int{404, 500} {
		old := stickerHTTPClient
		stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("synthetic")), Request: r}, nil
		})}
		kc, _ := newTestClient(t, nil)
		fake := &fakeKakao{}
		msg := events.StickerMessage{ChatID: testChatID, LogID: 99, AuthorID: testOtherID, Type: 20, Attachment: media.StickerAttachment{Path: "synthetic.webp"}}
		kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
			typed := remote.(*simplevent.Message[events.StickerMessage])
			result, err := typed.ConvertMessage(context.Background(), nil, nil)
			if status == 404 {
				if err != nil || result.Parts[0].Content.MsgType != event.MsgNotice {
					t.Fatal("missing asset needs explicit notice")
				}
				return bridgev2.EventHandlingResultSuccess
			}
			if !errors.Is(err, errStickerTransfer) {
				t.Fatal("server failure should remain transient")
			}
			return bridgev2.EventHandlingResultFailed.WithError(err)
		}
		handled := kc.handleEvent(fake, msg)
		if status == 404 {
			if !handled || len(fake.committed()) != 1 {
				t.Fatal("notice did not commit")
			}
		} else if handled || len(fake.committed()) != 0 {
			t.Fatal("transient failure committed cursor")
		}
		stickerHTTPClient = old
	}
}
