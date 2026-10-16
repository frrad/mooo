package connector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func callRecordPacket(t *testing.T, logID, authorID int64, sendAt int32, attachment *string) loco.Packet {
	t.Helper()
	log := bson.D{{Key: "logId", Value: logID}, {Key: "authorId", Value: authorID}, {Key: "type", Value: messagetype.Mvoip}, {Key: "sendAt", Value: sendAt}}
	if attachment != nil {
		log = append(log, bson.E{Key: "message", Value: *attachment}, bson.E{Key: "attachment", Value: *attachment})
	}
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: log}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: b}
}

// bridgeCallRecord sends one decoded call record through the client's real
// event path and returns the queued remote event and its converted content.
func bridgeCallRecord(t *testing.T, kc *KakaoClient, harness *testHarness, packet loco.Packet) (*simplevent.Message[events.CallMessage], *bridgev2.ConvertedMessage) {
	t.Helper()
	evt, err := events.DecodeForDelivery(packet)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeKakao{}
	before := len(harness.queued)
	if !kc.handleEvent(fake, evt) || len(fake.committed()) != 1 {
		t.Fatal("call record was not committed")
	}
	if len(harness.queued) != before+1 {
		t.Fatalf("call record queued %d events", len(harness.queued)-before)
	}
	msg, ok := harness.queued[before].(*simplevent.Message[events.CallMessage])
	if !ok {
		t.Fatalf("call record queued %T", harness.queued[before])
	}
	converted, err := msg.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return msg, converted
}

// The bridge posts the official Mac chat-list summary for each call record,
// from the record's author at its send time, with "viewer" meaning the bridged
// account (research/fixtures/calls/executed-mac-text.json).
func TestCallRecordsShowOfficialSummaryFromTheirAuthor(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name           string  `json:"name"`
			AuthoredBySelf bool    `json:"authored_by_self"`
			Attachment     *string `json:"attachment"`
			Expected       *struct {
				Summary string `json:"summary"`
			} `json:"expected"`
			MoooExpected *struct {
				Summary string `json:"summary"`
			} `json:"mooo_expected"`
		} `json:"cases"`
	}
	b, err := os.ReadFile("../../../research/fixtures/calls/executed-mac-text.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cases) == 0 {
		t.Fatal("executed call fixture unreadable")
	}
	kc, harness := newTestClient(t, nil)
	for i, c := range fixture.Cases {
		want := ""
		switch {
		case c.Expected != nil:
			want = c.Expected.Summary
		case c.MoooExpected != nil:
			want = c.MoooExpected.Summary
		default:
			t.Fatalf("%s: no expectation", c.Name)
		}
		author := testOtherID
		wantSender := bridgev2.EventSender{Sender: makeUserID(testOtherID)}
		if c.AuthoredBySelf {
			author = testSelfID
			wantSender = kc.selfSender()
		}
		logID := int64(1000 + i)
		sendAt := int32(1700000000 + i)
		msg, converted := bridgeCallRecord(t, kc, harness, callRecordPacket(t, logID, author, sendAt, c.Attachment))
		if msg.GetSender() != wantSender {
			t.Errorf("%s: sender = %+v, want %+v", c.Name, msg.GetSender(), wantSender)
		}
		if !msg.GetTimestamp().Equal(time.Unix(int64(sendAt), 0)) {
			t.Errorf("%s: timestamp = %v", c.Name, msg.GetTimestamp())
		}
		if msg.GetID() != makeMessageID(testChatID, logID) {
			t.Errorf("%s: message ID = %q", c.Name, msg.GetID())
		}
		content := convertedBody(t, converted)
		if content.MsgType != event.MsgNotice || content.Body != want {
			t.Errorf("%s: %s %q, want notice %q", c.Name, content.MsgType, content.Body, want)
		}
		metadata, ok := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
		if !ok || metadata.Type != messagetype.Mvoip || metadata.AuthorID != author || metadata.ChatID != testChatID || metadata.LogID != logID {
			t.Errorf("%s: metadata = %+v", c.Name, metadata)
		}
	}
}

// The raw message field of a call record duplicates its attachment, including
// call-server addresses and a call token. None of it reaches Matrix.
func TestCallRecordNeverBridgesRawText(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	attachment := `{"type":"bye","duration":80,"csIP":"192.0.2.1","csIP6":"2001:db8::1","cdrToken":"synthetic-token","callId":"1000000000000000001"}`
	_, converted := bridgeCallRecord(t, kc, harness, callRecordPacket(t, 50, testOtherID, 1700000000, &attachment))
	content := convertedBody(t, converted)
	for _, secret := range []string{"192.0.2.1", "2001:db8::1", "synthetic-token", "1000000000000000001", "{"} {
		if strings.Contains(content.Body, secret) || strings.Contains(content.FormattedBody, secret) {
			t.Fatalf("body %q leaks %q", content.Body, secret)
		}
	}
	if content.Body != "Voice Call 1:20" {
		t.Fatalf("body = %q", content.Body)
	}
}

// Names in a "joined the call" record come from stored member profiles; an
// unknown user renders as the official client's "(Unknown)".
func TestCallJoinRecordNamesStoredMembers(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	if err := kc.storeMemberProfiles(t.Context(), []chatmeta.Member{{UserID: 2000, Nickname: "Synthetic Two"}, {UserID: 4000, Nickname: "Synthetic Four"}}); err != nil {
		t.Fatal(err)
	}
	attachment := `{"type":"v_add","member":[2000,"4000",9999]}`
	_, converted := bridgeCallRecord(t, kc, harness, callRecordPacket(t, 60, testOtherID, 1700000000, &attachment))
	if body := convertedBody(t, converted).Body; body != "Synthetic Two, Synthetic Four, (Unknown) joined the Video Call" {
		t.Fatalf("body = %q", body)
	}
}

// The official reply shape for a call record is untraced, so a Matrix reply
// to one is refused before anything reaches KakaoTalk, with a clear reason.
func TestReplyToCallRecordIsRefused(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 37}}
	kc := connectedClient(t, fake)
	msg := matrixMessage(event.MsgText, "answer")
	msg.ReplyTo = &database.Message{
		ID:       makeMessageID(testChatID, 11),
		Room:     makePortalKey(testChatID, makeUserLoginID(testSelfID)),
		SenderID: makeUserID(testOtherID),
		Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, messagetype.Mvoip, "[call]", 0),
	}
	_, err := kc.HandleMatrixMessage(context.Background(), msg)
	var statusErr bridgev2.MessageStatus
	if !errors.Is(err, errCallReplyTarget) || !errors.As(err, &statusErr) || !statusErr.IsCertain || statusErr.Status != event.MessageStatusFail || statusErr.ErrorReason != event.MessageStatusUnsupported || !statusErr.SendNotice || !strings.Contains(statusErr.Message, "call") {
		t.Fatalf("status = %+v (err %v)", statusErr, err)
	}
	if len(fake.sends) != 0 || len(fake.replies) != 0 {
		t.Fatal("reply to a call record reached KakaoTalk")
	}
}
