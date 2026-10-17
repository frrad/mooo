package connector

import (
	"context"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

func TestUnsupportedMessageNoticeKeepsAuthorAndTimeAndPointsAtTheDump(t *testing.T) {
	for _, tc := range []struct {
		name         string
		debugDumpDir string
		wantBody     string
	}{
		{
			name:         "dump off",
			debugDumpDir: "",
			wantBody:     "A KakaoTalk message of unsupported type 98 was sent. Set network.debug_dump_dir in the bridge config to keep a private copy of messages like this.",
		},
		{
			name:         "dump on",
			debugDumpDir: t.TempDir(),
			wantBody:     "A KakaoTalk message of unsupported type 98 was sent. Its raw content was written to the bridge's private debug dump directory.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kc, harness := newTestClient(t, nil)
			kc.debugDumpDir = tc.debugDumpDir
			body, err := bson.Marshal(bson.D{
				{Key: "chatId", Value: testChatID},
				{Key: "chatLog", Value: bson.D{
					{Key: "logId", Value: int64(42)},
					{Key: "authorId", Value: testOtherID},
					{Key: "type", Value: int32(98)},
					{Key: "sendAt", Value: int32(1700000000)},
					{Key: "message", Value: "private text that must not be bridged"},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			evt, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeKakao{}

			kc.handleEvent(fake, evt)

			if len(harness.queued) != 1 {
				t.Fatalf("queued %d events, want 1", len(harness.queued))
			}
			msg := harness.queued[0].(*simplevent.Message[noticeData])
			if msg.GetSender() != (bridgev2.EventSender{Sender: makeUserID(testOtherID)}) {
				t.Errorf("sender = %+v, want the Kakao author", msg.GetSender())
			}
			if !msg.GetTimestamp().Equal(time.Unix(1700000000, 0)) {
				t.Errorf("timestamp = %v, want the Kakao send time", msg.GetTimestamp())
			}
			converted, err := msg.ConvertMessage(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if content := convertedBody(t, converted); content.MsgType != event.MsgNotice || content.Body != tc.wantBody {
				t.Errorf("content = %s %q, want notice %q", content.MsgType, content.Body, tc.wantBody)
			}
			metadata := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
			if metadata.AuthorID != 0 || metadata.valid() {
				t.Errorf("metadata = %+v; an unsupported notice must not be a reply target", metadata)
			}
			if len(fake.committed()) != 1 {
				t.Error("notice was not committed")
			}
		})
	}
}
