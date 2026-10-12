package connector

import (
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
)

func TestUnsupportedMessageText(t *testing.T) {
	for _, tc := range []struct {
		evt  events.UnsupportedMessage
		want string
	}{
		{events.UnsupportedMessage{Type: 51, Attachment: `{"type":"bye","duration":95}`}, "📞 Call ended (1m35s)"},
		{events.UnsupportedMessage{Type: 51, Attachment: `{"type":"v_noanswer"}`}, "🎥 Video call missed"},
		{events.UnsupportedMessage{Type: 71, Message: "fallback", Attachment: `{"P":{"ME":"선물을 보냈어요.","SNM":"카카오톡 선물하기"}}`}, "[카카오톡 선물하기] 선물을 보냈어요."},
		{events.UnsupportedMessage{Type: 71, Message: "only text"}, "only text"},
		{events.UnsupportedMessage{Type: 98}, "A KakaoTalk message of unsupported type 98 was sent."},
	} {
		if got := unsupportedMessageText(tc.evt); got != tc.want {
			t.Errorf("type %d: got %q, want %q", tc.evt.Type, got, tc.want)
		}
	}
}
