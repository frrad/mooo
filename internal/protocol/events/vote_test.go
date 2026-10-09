package events

import "testing"

func TestNilVoteHasNoPosition(t *testing.T) {
	var m *VoteMessage
	if _, _, ok := MessagePosition(m); ok {
		t.Fatal("nil poll has a cursor")
	}
}
