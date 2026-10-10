package events

import (
	"encoding/json"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type observedReplyCase struct {
	Name       string         `json:"name"`
	Message    string         `json:"message"`
	Attachment map[string]any `json:"attachment"`
}

func observedReplyCases(t *testing.T) []observedReplyCase {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/replies/observed-group-reply-shapes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []observedReplyCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Cases
}

func decodeObservedReply(t *testing.T, c observedReplyCase) ReplyMessage {
	t.Helper()
	attachment, err := json.Marshal(c.Attachment)
	if err != nil {
		t.Fatal(err)
	}
	evt, err := Decode(packet(t, "MSG", bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{
			{Key: "logId", Value: int64(201)}, {Key: "type", Value: int32(26)}, {Key: "authorId", Value: int64(8)},
			{Key: "sendAt", Value: int32(1300)}, {Key: "message", Value: c.Message}, {Key: "attachment", Value: string(attachment)},
		}},
	}))
	reply, ok := evt.(ReplyMessage)
	if err != nil || !ok {
		t.Fatalf("%s: event=%T err=%v", c.Name, evt, err)
	}
	return reply
}

func TestDecodeObservedGroupReplyShapes(t *testing.T) {
	cases := observedReplyCases(t)
	if len(cases) != 3 {
		t.Fatalf("fixture cases = %d", len(cases))
	}
	text := decodeObservedReply(t, cases[0])
	if text.Source.Type != 2 || text.Source.Message != "Photo" || text.Attachment.Type != 0 || text.Attachment.Sticker != nil {
		t.Fatalf("text reply to photo = %#v", text)
	}
	for _, c := range cases[1:] {
		reply := decodeObservedReply(t, c)
		if reply.Attachment.Type != 12 || !reply.Attachment.Only || reply.Attachment.Sticker == nil || reply.Attachment.Sticker.Path != c.Attachment["emoticonItemPath"] {
			t.Fatalf("%s: attachment = %#v", c.Name, reply.Attachment)
		}
		if reply.Message != "(Emoticons)" || reply.Source.LogID <= 0 {
			t.Fatalf("%s: reply = %#v", c.Name, reply)
		}
	}
}

// An attachment type the bridge cannot render is kept as an explicit marker
// rather than being dropped while the reply decodes.
func TestDecodeReplyKeepsUnsupportedAttachmentType(t *testing.T) {
	reply := decodeObservedReply(t, observedReplyCase{Name: "unsupported", Message: "", Attachment: map[string]any{
		"src_logId": 99, "src_userId": 7, "src_type": 1, "src_message": "source", "attach_only": true, "attach_type": 71,
		"attach_content": map[string]any{"synthetic": true},
	}})
	if reply.Attachment.Type != 71 || reply.Attachment.Sticker != nil || !reply.Attachment.Only {
		t.Fatalf("attachment = %#v", reply.Attachment)
	}
}
