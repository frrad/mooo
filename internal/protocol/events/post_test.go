package events

import "testing"

func TestNilPostHasNoMessagePosition(t *testing.T) {
	var m *PostMessage
	if _, _, ok := MessagePosition(m); ok {
		t.Fatal("nil post has a cursor")
	}
}
