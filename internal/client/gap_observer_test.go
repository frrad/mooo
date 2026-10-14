package client

import (
	"bytes"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

func gapTestChatLog(t *testing.T, logID int64, typ int32, field string, value string) bson.Raw {
	t.Helper()
	log, err := bson.Marshal(bson.D{{Key: "logId", Value: logID}, {Key: "type", Value: typ}, {Key: field, Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func gapTestPacket(t *testing.T, log bson.Raw) loco.Packet {
	t.Helper()
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: log}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
}

// The observer sees exactly the gaps, each with the raw body it came from.
func TestGapObserverReceivesPushGapWithBody(t *testing.T) {
	var got []GapObservation
	gaps := newGapFunc(func(o GapObservation) { got = append(got, o) })
	malformed := gapTestPacket(t, gapTestChatLog(t, 101, 2, "attachment", "{synthetic malformed"))
	text := gapTestPacket(t, gapTestChatLog(t, 102, 1, "message", "synthetic text"))
	raw := make(chan loco.Packet, 2)
	raw <- malformed
	raw <- text
	close(raw)
	output := make(chan events.Result, 2)
	decodeEventStreamWithTerminalStop(raw, output, nil, func(int64, int64) {}, nil, nil, gaps)

	if len(got) != 1 {
		t.Fatalf("observations = %d, want 1", len(got))
	}
	if got[0].Source != "MSG" || got[0].Gap.LogID != 101 || got[0].Gap.Reason != "attachment_json" || !bytes.Equal(got[0].Body, malformed.Body) {
		t.Fatalf("observation = %+v", got[0])
	}
	malformed.Body[len(malformed.Body)-2] ^= 0xff
	if bytes.Equal(got[0].Body, malformed.Body) {
		t.Fatal("observation shares the packet's body instead of copying it")
	}
}

func TestGapObserverReceivesHistoryGapWithChatLog(t *testing.T) {
	var got []GapObservation
	gaps := newGapFunc(func(o GapObservation) { got = append(got, o) })
	log := gapTestChatLog(t, 101, 2, "attachment", "{synthetic malformed")
	page, err := decodeHistoryPage(syncmsg.Request{ChatID: 42, Cur: 100, Max: 200}, syncmsg.Response{ChatLogs: []bson.Raw{log}}, 10, gaps)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	if len(got) != 1 || got[0].Source != "SYNCMSG" || !bytes.Equal(got[0].Body, log) {
		t.Fatalf("observations = %+v", got)
	}
}

// A message that decodes but has no decoder for its content, as a whole or as
// a reply attachment, is observed with its body so its shape can be studied.
// Supported messages, including sticker replies, are not.
func TestGapObserverReceivesUndisplayableMessages(t *testing.T) {
	var got []GapObservation
	gaps := newGapFunc(func(o GapObservation) { got = append(got, o) })
	unsupported := gapTestPacket(t, gapTestChatLog(t, 101, 71, "message", "synthetic leverage"))
	replyLog := func(logID int64, attachment string) loco.Packet {
		log, err := bson.Marshal(bson.D{{Key: "logId", Value: logID}, {Key: "type", Value: int32(26)}, {Key: "authorId", Value: int64(8)},
			{Key: "sendAt", Value: int32(1300)}, {Key: "message", Value: ""}, {Key: "attachment", Value: attachment}})
		if err != nil {
			t.Fatal(err)
		}
		return gapTestPacket(t, log)
	}
	attachmentReply := replyLog(102, `{"src_logId":99,"src_userId":7,"src_type":1,"src_message":"source","attach_only":true,"attach_type":71,"attach_content":{"synthetic":true}}`)
	textReply := replyLog(103, `{"src_logId":99,"src_userId":7,"src_type":1,"src_message":"source"}`)
	raw := make(chan loco.Packet, 3)
	raw <- unsupported
	raw <- attachmentReply
	raw <- textReply
	close(raw)
	output := make(chan events.Result, 3)
	decodeEventStreamWithTerminalStop(raw, output, nil, func(int64, int64) {}, nil, nil, gaps)

	if len(got) != 2 {
		t.Fatalf("observations = %+v, want 2", got)
	}
	if got[0].Gap.LogID != 101 || got[0].Gap.Type != 71 || got[0].Gap.Reason != GapReasonUnsupportedType || !bytes.Equal(got[0].Body, unsupported.Body) {
		t.Fatalf("unsupported observation = %+v", got[0])
	}
	if got[1].Gap.LogID != 102 || got[1].Gap.Type != 26 || got[1].Gap.Reason != GapReasonUnsupportedReplyAttachment || !bytes.Equal(got[1].Body, attachmentReply.Body) {
		t.Fatalf("reply observation = %+v", got[1])
	}
}

func TestNilGapObserverAddsNoWork(t *testing.T) {
	if newGapFunc(nil) != nil {
		t.Fatal("nil observer produced a gap function")
	}
	var gaps gapFunc
	gaps.observe("MSG", events.MessageGap{}, nil)
}
