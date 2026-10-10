package events

import (
	"encoding/json"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
)

type editDeleteFixture struct {
	Push struct {
		Edit struct {
			Method string
			Body   json.RawMessage
		} `json:"edit"`
		Delete struct {
			Method string
			Body   json.RawMessage
		} `json:"delete"`
	} `json:"push"`
	CatchUp []json.RawMessage `json:"catchUp"`
}

func loadEditDeleteFixture(t *testing.T) editDeleteFixture {
	t.Helper()
	data, err := os.ReadFile("../../../research/fixtures/edits/observed-group-edit-delete.json")
	if err != nil {
		t.Fatal(err)
	}
	var f editDeleteFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func extJSONToBSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var doc bson.D
	if err := bson.UnmarshalExtJSON(raw, false, &doc); err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// catchUpPacket wraps one SYNCMSG chat log the way catch-up delivers it.
func catchUpPacket(t *testing.T, raw json.RawMessage) loco.Packet {
	t.Helper()
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(3000)}, {Key: "chatLog", Value: bson.Raw(extJSONToBSON(t, raw))}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
}

func TestObservedEditPushDecodesToEditWithModifiedText(t *testing.T) {
	f := loadEditDeleteFixture(t)
	event, err := DecodeForDelivery(loco.Packet{Header: loco.Header{Method: f.Push.Edit.Method}, Body: extJSONToBSON(t, f.Push.Edit.Body)})
	if err != nil {
		t.Fatal(err)
	}
	edit, ok := event.(MessageEdited)
	if !ok {
		t.Fatalf("event = %T", event)
	}
	if edit.ChatID != 3000 || edit.LogID != 3000000000000000202 || edit.TargetLogID != 3000000000000000201 || edit.TargetRevision != 1 || edit.AuthorID != 2000 {
		t.Fatalf("edit = %+v", edit)
	}
	modified, ok := edit.Modified.(TextMessage)
	if !ok || modified.Message != "synthetic original-edited" || modified.LogID != 3000000000000000201 || modified.Revision != 1 {
		t.Fatalf("modified = %#v", edit.Modified)
	}
	if chatID, logID, ok := MessagePosition(event); !ok || chatID != 3000 || logID != 3000000000000000202 {
		t.Fatal("edit feed does not report its own log position")
	}
}

func TestObservedDeletePushDecodesToDeletion(t *testing.T) {
	f := loadEditDeleteFixture(t)
	event, err := DecodeForDelivery(loco.Packet{Header: loco.Header{Method: f.Push.Delete.Method}, Body: extJSONToBSON(t, f.Push.Delete.Body)})
	if err != nil {
		t.Fatal(err)
	}
	deletion, ok := event.(MessageDeleted)
	if !ok {
		t.Fatalf("event = %T", event)
	}
	if deletion.ChatID != 3000 || deletion.LogID != 3000000000000000204 || deletion.TargetLogID != 3000000000000000203 || deletion.AuthorID != 2000 || deletion.ByHost {
		t.Fatalf("deletion = %+v", deletion)
	}
	if _, logID, ok := MessagePosition(event); !ok || logID != 3000000000000000204 {
		t.Fatal("delete feed does not report its own log position")
	}
}

func TestObservedCatchUpEditAndDeleteLogs(t *testing.T) {
	f := loadEditDeleteFixture(t)
	var got []Event
	for _, raw := range f.CatchUp {
		event, err := DecodeForDelivery(catchUpPacket(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if text, ok := got[0].(TextMessage); !ok || text.Message != "synthetic original-edited" || text.Revision != 1 {
		t.Fatalf("edited message = %#v", got[0])
	}
	if edit, ok := got[1].(MessageEdited); !ok || edit.TargetLogID != 3000000000000000201 || edit.TargetRevision != 1 || edit.Modified != nil {
		t.Fatalf("edit feed = %#v", got[1])
	}
	deleted, ok := got[2].(DeletedMessage)
	if !ok || deleted.LogID != 3000000000000000203 || deleted.AuthorID != 2000 || deleted.BaseType != 1 {
		t.Fatalf("deleted message = %#v", got[2])
	}
	if s := deleted.String(); s == "" {
		t.Fatal("deleted message has no redacted string form")
	}
	if deletion, ok := got[3].(MessageDeleted); !ok || deletion.TargetLogID != 3000000000000000203 {
		t.Fatalf("delete feed = %#v", got[3])
	}
}
