package chat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestReplyRequestMarshalBSON(t *testing.T) {
	body, err := (ReplyRequest{
		ChatID: 42, Message: "reply text",
		Target: ReplyTarget{LogID: 99, UserID: 7, Type: TextType, Message: "source text"},
	}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("chatId").Int64() != 42 || raw.Lookup("type").Int32() != ReplyType || raw.Lookup("msg").StringValue() != "reply text" || raw.Lookup("noSeen").Boolean() {
		t.Fatalf("unexpected reply WRITE: %v", raw)
	}
	elements, err := raw.Elements()
	if err != nil || len(elements) != 5 || elements[4].Key() != "extra" {
		t.Fatalf("reply WRITE keys = %v, err=%v", elements, err)
	}
	var attachment map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw.Lookup("extra").StringValue()))
	decoder.UseNumber()
	if err := decoder.Decode(&attachment); err != nil {
		t.Fatal(err)
	}
	if len(attachment) != 5 || attachment["src_logId"].(json.Number).String() != "99" || attachment["src_userId"].(json.Number).String() != "7" || attachment["src_type"].(json.Number).String() != "1" || attachment["src_message"] != "source text" {
		t.Fatalf("attachment = %#v", attachment)
	}
	if spoilers, ok := attachment["src_spoilers"].([]any); !ok || len(spoilers) != 0 {
		t.Fatalf("src_spoilers = %#v", attachment["src_spoilers"])
	}
}

func TestReplyPreviewUsesOneHundredUTF16Units(t *testing.T) {
	source := strings.Repeat("a", 99) + "😀tail"
	preview := truncateReplyPreview(source)
	if got := len(utf16.Encode([]rune(preview))); got != 99 {
		t.Fatalf("preview UTF-16 units = %d, want 99 without a split surrogate", got)
	}
	if preview != strings.Repeat("a", 99) {
		t.Fatalf("preview = %q", preview)
	}
}

func TestReplyRequestRejectsInvalidTarget(t *testing.T) {
	for _, target := range []ReplyTarget{
		{},
		{LogID: 1, UserID: 0, Type: 1},
		{LogID: 1, UserID: 2, Type: 0},
		{LogID: 1, UserID: 2, Type: 1, LinkID: -1},
		{LogID: 1, UserID: 2, Type: 1, Message: "bad\x00message"},
	} {
		if _, err := (ReplyRequest{ChatID: 42, Message: "reply", Target: target}).MarshalBSON(); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("target %#v error = %v", target, err)
		}
	}
}
