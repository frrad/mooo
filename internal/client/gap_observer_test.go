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

func TestNilGapObserverAddsNoWork(t *testing.T) {
	if newGapFunc(nil) != nil {
		t.Fatal("nil observer produced a gap function")
	}
	var gaps gapFunc
	gaps.observe("MSG", events.MessageGap{}, nil)
}
