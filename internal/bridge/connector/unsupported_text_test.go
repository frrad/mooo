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
		{events.UnsupportedMessage{Type: 98, Message: "own text"}, "own text"},
		{events.UnsupportedMessage{Type: 98}, "A KakaoTalk message of unsupported type 98 was sent."},
	} {
		if got := unsupportedMessageText(tc.evt); got != tc.want {
			t.Errorf("type %d: got %q, want %q", tc.evt.Type, got, tc.want)
		}
	}
}
