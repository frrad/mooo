package client

import (
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestMalformedMembershipCannotBeSkippedBeforeLaterContent(t *testing.T) {
	for _, method := range []string{"DELMEM", "NEWMEM", "LEFT"} {
		t.Run(method, func(t *testing.T) {
			raw := make(chan loco.Packet, 2)
			out := make(chan events.Result, 2)
			raw <- loco.Packet{Header: loco.Header{Method: method}, Body: []byte{1, 2, 3}}
			valid, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic after membership failure"}}}})
			if err != nil {
				t.Fatal(err)
			}
			raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: valid}
			close(raw)
			terminal := false
			decodeEventStreamWithTerminal(raw, out, nil, nil, func() { terminal = true })
			first, ok := <-out
			if !ok || first.Err == nil {
				t.Fatal("missing membership error")
			}
			if _, ok = <-out; ok {
				t.Fatal("later content admitted after undecodable membership")
			}
			if !terminal {
				t.Fatal("membership failure did not terminate admission")
			}
		})
	}
}
