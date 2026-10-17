package connector

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func leverageCardPacket(t *testing.T, logID, authorID int64, sendAt int32, message, attachment *string) loco.Packet {
	t.Helper()
	log := bson.D{{Key: "logId", Value: logID}, {Key: "authorId", Value: authorID}, {Key: "type", Value: messagetype.Leverage}, {Key: "sendAt", Value: sendAt}}
	if message != nil {
		log = append(log, bson.E{Key: "message", Value: *message})
	}
	if attachment != nil {
		log = append(log, bson.E{Key: "attachment", Value: *attachment})
	}
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: log}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: b}
}

// queuedLeverageMessage is the queued remote event of a type-71 chat log.
type queuedLeverageMessage interface {
	bridgev2.RemoteMessage
	bridgev2.RemoteEventWithTimestamp
}

// bridgeLeverageCard sends one decoded type-71 chat log through the client's
// real event path and returns the queued remote event and its conversion.
func bridgeLeverageCard(t *testing.T, kc *KakaoClient, harness *testHarness, packet loco.Packet) (queuedLeverageMessage, *bridgev2.ConvertedMessage) {
	t.Helper()
	evt, err := events.DecodeForDelivery(packet)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeKakao{}
	before := len(harness.queued)
	if !kc.handleEvent(fake, evt) || len(fake.committed()) != 1 {
		t.Fatal("card was not committed")
	}
	if len(harness.queued) != before+1 {
		t.Fatalf("card queued %d events", len(harness.queued)-before)
	}
	msg, ok := harness.queued[before].(queuedLeverageMessage)
	if !ok {
		t.Fatalf("card queued %T", harness.queued[before])
	}
	converted, err := msg.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return msg, converted
}

func requireLeverageCard(t *testing.T, kc *KakaoClient, msg queuedLeverageMessage, converted *bridgev2.ConvertedMessage, author, logID int64, sendAt int32, wantBody string, wantReply bool) {
	t.Helper()
	wantSender := bridgev2.EventSender{Sender: makeUserID(author)}
	if author == testSelfID {
		wantSender = kc.selfSender()
	}
	if msg.GetSender() != wantSender {
		t.Errorf("sender = %+v, want %+v", msg.GetSender(), wantSender)
	}
	if !msg.GetTimestamp().Equal(time.Unix(int64(sendAt), 0)) {
		t.Errorf("timestamp = %v", msg.GetTimestamp())
	}
	if msg.GetID() != makeMessageID(testChatID, logID) {
		t.Errorf("message ID = %q", msg.GetID())
	}
	content := convertedBody(t, converted)
	if content.MsgType != event.MsgText || content.Body != wantBody || content.FormattedBody != "" {
		t.Errorf("content = %s %q (formatted %q), want text %q", content.MsgType, content.Body, content.FormattedBody, wantBody)
	}
	metadata, ok := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
	if !ok || metadata.Type != messagetype.Leverage || metadata.AuthorID != author || metadata.ChatID != testChatID || metadata.LogID != logID || metadata.ReplyUnavailable == wantReply {
		t.Errorf("metadata = %+v replyUnavailable=%v", metadata, metadata != nil && metadata.ReplyUnavailable)
	}
}

// Every executed case reaches Matrix as the official summary from the card's
// author at its send time, with the tap link on its own line, or as the
// generic notice where mooo deliberately keeps it
// (research/fixtures/leverage/executed-mac-model.json).
func TestExecutedLeverageCardsBridgeTheOfficialSummary(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name           string  `json:"name"`
			AuthoredBySelf bool    `json:"authored_by_self"`
			Message        *string `json:"message"`
			Attachment     *string `json:"attachment"`
			Expected       struct {
				Summary           *string `json:"summary"`
				ReplyAvailable    bool    `json:"reply_available"`
				ReactionAvailable bool    `json:"reaction_available"`
				PublicLink        *string `json:"public_link"`
			} `json:"expected"`
			MoooExpected *struct {
				Kind     string  `json:"kind"`
				Summary  string  `json:"summary"`
				Link     *string `json:"link"`
				Reply    bool    `json:"reply"`
				Reaction bool    `json:"reaction"`
			} `json:"mooo_expected"`
		} `json:"cases"`
	}
	b, err := os.ReadFile("../../../research/fixtures/leverage/executed-mac-model.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cases) < 80 {
		t.Fatal("executed leverage fixture unreadable")
	}
	kc, harness := newTestClient(t, nil)
	for i, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			kind, summary, link, reply, reaction := "leverage", "", c.Expected.PublicLink, c.Expected.ReplyAvailable, c.Expected.ReactionAvailable
			if c.Expected.Summary != nil {
				summary = *c.Expected.Summary
			}
			if m := c.MoooExpected; m != nil {
				kind, summary, link, reply, reaction = m.Kind, m.Summary, m.Link, m.Reply, m.Reaction
			}
			author := testOtherID
			if c.AuthoredBySelf {
				author = testSelfID
			}
			logID, sendAt := int64(1000+i), int32(1700000000+i)
			msg, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, logID, author, sendAt, c.Message, c.Attachment))
			if kind == "unsupported" {
				if body := convertedBody(t, converted).Body; body != "A KakaoTalk message of unsupported type 71 was sent. Set network.debug_dump_dir in the bridge config to keep a private copy of messages like this." {
					t.Fatalf("body = %q", body)
				}
				wantSender := bridgev2.EventSender{Sender: makeUserID(author)}
				if author == testSelfID {
					wantSender = kc.selfSender()
				}
				if msg.GetSender() != wantSender || !msg.GetTimestamp().Equal(time.Unix(int64(sendAt), 0)) {
					t.Errorf("sender = %+v, timestamp = %v; want the card's author and time", msg.GetSender(), msg.GetTimestamp())
				}
				return
			}
			body := summary
			if link != nil {
				body += "\n" + *link
			}
			requireLeverageCard(t, kc, msg, converted, author, logID, sendAt, body, reply)
			if metadata := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata); metadata.ReactionUnavailable == reaction {
				t.Errorf("reactionUnavailable = %v, want reaction %v", metadata.ReactionUnavailable, reaction)
			}
		})
	}
}

// Cards shared and forwarded between owned accounts bridge as their chat log
// message and link (research/fixtures/leverage/observed-cards.json).
func TestObservedLeverageCardsBridge(t *testing.T) {
	fixture := readObservedLeverageFixture(t)
	kc, harness := newTestClient(t, nil)
	for i, c := range fixture.Cards {
		author := testOtherID
		if c.AuthoredBySelf {
			author = testSelfID
		}
		logID, sendAt := int64(2000+i), int32(1700001000+i)
		msg, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, logID, author, sendAt, &c.Message, &c.Attachment))
		requireLeverageCard(t, kc, msg, converted, author, logID, sendAt, c.Expected.MoooBody, c.Expected.Reply)
	}
}

type observedLeverageFixture struct {
	Cards []struct {
		Name           string `json:"name"`
		AuthoredBySelf bool   `json:"authored_by_self"`
		Message        string `json:"message"`
		Attachment     string `json:"attachment"`
		Expected       struct {
			MoooBody string `json:"mooo_body"`
			Reply    bool   `json:"reply"`
		} `json:"expected"`
	} `json:"cards"`
	Reply struct {
		Message     string   `json:"message"`
		ExtraKeys   []string `json:"extra_keys"`
		SrcType     int32    `json:"src_type"`
		SrcMessage  string   `json:"src_message"`
		SrcSpoilers []any    `json:"src_spoilers"`
	} `json:"reply"`
}

func readObservedLeverageFixture(t *testing.T) observedLeverageFixture {
	t.Helper()
	var fixture observedLeverageFixture
	b, err := os.ReadFile("../../../research/fixtures/leverage/observed-cards.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cards) == 0 {
		t.Fatal("observed leverage fixture unreadable")
	}
	return fixture
}

// A Matrix reply to a bridged card sends what the official Android client
// sent when replying to the same card: src_type 71 and the card's chat log
// message as src_message, with the same extra keys.
func TestReplyToLeverageCardMatchesOfficialReply(t *testing.T) {
	fixture := readObservedLeverageFixture(t)
	source := fixture.Cards[0]
	kc, harness := newTestClient(t, nil)
	_, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, 11, testOtherID, 1700000000, &source.Message, &source.Attachment))

	fake := &fakeKakao{sendResp: chat.WriteResponse{ChatID: testChatID, LogID: 33, SendAt: 1700000003}}
	kc = connectedClient(t, fake)
	msg := matrixMessage(event.MsgText, fixture.Reply.Message)
	msg.ReplyTo = &database.Message{
		ID:       makeMessageID(testChatID, 11),
		Room:     makePortalKey(testChatID, makeUserLoginID(testSelfID)),
		SenderID: makeUserID(testOtherID),
		Metadata: converted.Parts[0].DBMetadata,
	}
	if _, err := kc.HandleMatrixMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(fake.sends) != 0 || len(fake.replies) != 1 {
		t.Fatalf("text sends = %d, replies = %d", len(fake.sends), len(fake.replies))
	}
	b, err := bson.Marshal(fake.replies[0].request)
	if err != nil {
		t.Fatal(err)
	}
	var write struct {
		Message string `bson:"msg"`
		Type    int32  `bson:"type"`
		Extra   string `bson:"extra"`
	}
	if bson.Unmarshal(b, &write) != nil || write.Type != messagetype.Reply || write.Message != fixture.Reply.Message {
		t.Fatalf("write = %+v", write)
	}
	var extra map[string]json.RawMessage
	if json.Unmarshal([]byte(write.Extra), &extra) != nil {
		t.Fatalf("extra = %q", write.Extra)
	}
	keys := slices.Sorted(maps.Keys(extra))
	if !slices.Equal(keys, fixture.Reply.ExtraKeys) {
		t.Fatalf("extra keys = %v, official %v", keys, fixture.Reply.ExtraKeys)
	}
	var srcType int32
	var srcMessage string
	var spoilers []any
	if json.Unmarshal(extra["src_type"], &srcType) != nil || json.Unmarshal(extra["src_message"], &srcMessage) != nil || json.Unmarshal(extra["src_spoilers"], &spoilers) != nil {
		t.Fatalf("extra = %q", write.Extra)
	}
	if srcType != fixture.Reply.SrcType || srcMessage != fixture.Reply.SrcMessage || len(spoilers) != len(fixture.Reply.SrcSpoilers) {
		t.Fatalf("src_type %d src_message %q spoilers %v; official %d %q %v", srcType, srcMessage, spoilers, fixture.Reply.SrcType, fixture.Reply.SrcMessage, fixture.Reply.SrcSpoilers)
	}
}

// The official client offers no Reply on locked or adult-only cards, so a
// Matrix reply to one is refused before anything reaches KakaoTalk.
func TestReplyToLockedLeverageCardIsRefused(t *testing.T) {
	for _, flag := range []string{"LOCK", "ADULT"} {
		message := "Synthetic card summary"
		attachment := `{"P":{"` + flag + `":true,"L":{"LPC":"https://example.com/card"}}}`
		kc, harness := newTestClient(t, nil)
		_, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, 11, testOtherID, 1700000000, &message, &attachment))

		fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 37}}
		kc = connectedClient(t, fake)
		msg := matrixMessage(event.MsgText, "answer")
		msg.ReplyTo = &database.Message{
			ID:       makeMessageID(testChatID, 11),
			Room:     makePortalKey(testChatID, makeUserLoginID(testSelfID)),
			SenderID: makeUserID(testOtherID),
			Metadata: converted.Parts[0].DBMetadata,
		}
		_, err := kc.HandleMatrixMessage(context.Background(), msg)
		var statusErr bridgev2.MessageStatus
		if !errors.Is(err, errLeverageReplyTarget) || !errors.As(err, &statusErr) || !statusErr.IsCertain || statusErr.Status != event.MessageStatusFail || statusErr.ErrorReason != event.MessageStatusUnsupported || !statusErr.SendNotice || !strings.Contains(statusErr.Message, "card") {
			t.Fatalf("%s: status = %+v (err %v)", flag, statusErr, err)
		}
		if len(fake.sends) != 0 || len(fake.replies) != 0 {
			t.Fatalf("%s: reply to a locked card reached KakaoTalk", flag)
		}
	}
}

// A Kakao reply to a card quotes the card's text, which the official client
// shows as the quote too (src_message is the card's chat log message).
func TestInboundReplyToUnbridgedLeverageCardQuotesItsSummary(t *testing.T) {
	fixture := readObservedLeverageFixture(t)
	kc, intent := newReplyFramework(t)
	reply := events.ReplyMessage{ChatID: testChatID, LogID: 212, AuthorID: testOtherID, SentAt: 1700000212, Message: fixture.Reply.Message,
		Source: events.ReplySource{LogID: 151, UserID: testOtherID, Type: fixture.Reply.SrcType, Message: fixture.Reply.SrcMessage}}
	if !kc.handleEvent(&fakeKakao{}, reply) {
		t.Fatal("reply was not committed")
	}
	if len(intent.sentContents()) != 1 {
		t.Fatalf("Matrix events = %d, want 1", len(intent.sentContents()))
	}
	if got := intent.sentContents()[0].Body; got != "> "+fixture.Reply.SrcMessage+"\n\n"+fixture.Reply.Message {
		t.Fatalf("body = %q", got)
	}
}

// Deleting a card for everyone is untraced in the official client, so a
// Matrix redaction of the account's own card stays local.
func TestRedactingOwnLeverageCardIsRefused(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	target := ownTarget(205, messagetype.Leverage, time.Now().Add(-time.Hour))
	err := kc.HandleMatrixMessageRemove(context.Background(), matrixRemove(target, "$redact-card"))
	requireStatus(t, err, event.MessageStatusFail, true)
	if !errors.Is(err, errLeverageDeleteNotSupported) || len(fake.deletes) != 0 {
		t.Fatalf("err=%v deletes=%d", err, len(fake.deletes))
	}
}

// The official client offers no reaction on carousel cards, so a Matrix
// reaction to one is refused before anything reaches KakaoTalk; a feed card
// in the same chat still takes reactions.
func TestReactionToCarouselLeverageCardIsRefused(t *testing.T) {
	message := "Synthetic card summary"
	for _, c := range []struct {
		tp      string
		refused bool
	}{{"Carousel", true}, {"CAROUSEL", true}, {"Feed", false}} {
		attachment := `{"P":{"TP":"` + c.tp + `","L":{"LPC":"https://example.com/card"}},"C":{}}`
		kc, harness := newTestClient(t, nil)
		_, converted := bridgeLeverageCard(t, kc, harness, leverageCardPacket(t, 11, testOtherID, 1700000000, &message, &attachment))
		target := &database.Message{
			ID: makeMessageID(testChatID, 11), Room: makePortalKey(testChatID, kc.login.ID),
			SenderID: makeUserID(testOtherID), Metadata: converted.Parts[0].DBMetadata,
		}
		_, _, err := validateMatrixReaction(kc, leverageReaction(kc, target))
		if !c.refused {
			if err != nil {
				t.Fatalf("%s: reaction refused: %v", c.tp, err)
			}
			continue
		}
		var statusErr bridgev2.MessageStatus
		if !errors.Is(err, errLeverageReactionTarget) || !errors.As(err, &statusErr) || !statusErr.IsCertain || statusErr.Status != event.MessageStatusFail || !strings.Contains(statusErr.Message, "carousel") {
			t.Fatalf("%s: status = %+v (err %v)", c.tp, statusErr, err)
		}
	}
}

func leverageReaction(kc *KakaoClient, target *database.Message) *bridgev2.MatrixReaction {
	msg := &bridgev2.MatrixReaction{TargetMessage: target}
	msg.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID)}}
	msg.Event = &event.Event{Sender: kc.login.UserMXID}
	msg.Content = &event.ReactionEventContent{RelatesTo: event.RelatesTo{Type: event.RelAnnotation, Key: "👍"}}
	return msg
}
