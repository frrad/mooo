package connector

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// A rich (photo) announcement replaces the topic with its text summary
// instead of leaving the previous announcement in place; a later poll
// announcement replaces it again, and removal clears it.
func TestRichAnnouncementSnapshotsReplaceAndClearTopic(t *testing.T) {
	fake := &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat", ChatMetas: []chatmeta.ChatMeta{{Type: 3, Content: "Synthetic group"}}}}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	p := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID)), Metadata: &KakaoPortalMetadata{}}}
	steps := []struct {
		revision int64
		content  string
		want     string
	}{
		{50, `{"type":"IMAGE","notice":true,"content":"Synthetic rich announcement","json_content":"[{\"type\":\"text\",\"text\":\"Synthetic rich announcement\"}]","thumbnail":"https://example.invalid/t.jpg"}`, "Synthetic rich announcement"},
		{51, `{"type":"POLL","notice":true,"subject":"Synthetic announcement poll","poll_count":1}`, "Synthetic announcement poll"},
		{52, `{}`, ""},
	}
	for _, step := range steps {
		fake.moimResponse = chatmeta.MoimResponse{ChatID: testChatID, Metas: []chatmeta.MoimMeta{{Type: 1, UpdateRevision: step.revision, Content: step.content}}}
		info, err := kc.GetChatInfo(context.Background(), p)
		if err != nil || info.Topic == nil || *info.Topic != step.want {
			t.Fatalf("revision %d: topic=%v err=%v, want %q", step.revision, info.Topic, err, step.want)
		}
		info.ExtraUpdates(context.Background(), p)
	}
}

// Boards posts with photo and poll objects (observed on announcement posts)
// keep their text and summarize the rich objects, instead of becoming
// malformed-payload notices.
func TestObservedRichPostsSummarizeInsteadOfMalformedNotice(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/post/observed-rich-objects.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name       string         `json:"name"`
			Attachment map[string]any `json:"attachment"`
			Expected   string         `json:"mooo_expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) != 2 {
		t.Fatalf("fixture: %v", err)
	}
	for i, c := range fixture.Cases {
		attachment, _ := json.Marshal(c.Attachment)
		body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{
			{Key: "logId", Value: int64(500 + i)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: int32(24)},
			{Key: "message", Value: "[Announcement] synthetic"}, {Key: "attachment", Value: string(attachment)},
		}}})
		decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		post, ok := decoded.(events.PostMessage)
		if !ok {
			t.Fatalf("%s: decoded %T, want PostMessage", c.Name, decoded)
		}
		converted, err := convertPost(context.Background(), nil, nil, post)
		if err != nil {
			t.Fatal(err)
		}
		if got := converted.Parts[0].Content.Body; got != c.Expected {
			t.Fatalf("%s: body %q, want %q", c.Name, got, c.Expected)
		}
	}
}
