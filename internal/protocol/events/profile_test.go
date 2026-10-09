package events

import (
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestProfileRejectsAmbiguousOrInvalidAttachment(t *testing.T) {
	for _, a := range []string{`{"userId":42,"nickName":"one","userId":43}`, `{"userId":0,"nickName":"one"}`, `{"userId":42,"nickName":""}`, `{"userId":42,"nickName":"one"} {}`, `{"userId":42,"nickName":"one","statusMessage":7}`} {
		p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Profile}, {Key: "attachment", Value: a}}}})
		if _, err := Decode(p); err != ErrMalformedEvent {
			t.Fatal("invalid profile accepted")
		}
	}
	var m *ProfileMessage
	if _, _, ok := MessagePosition(m); ok {
		t.Fatal("nil profile identity invented")
	}
}
