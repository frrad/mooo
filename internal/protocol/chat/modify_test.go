package chat

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/messagetype"
)

func TestModifyRequestCarriesTheOriginalTypeAndNewText(t *testing.T) {
	body, err := (ModifyRequest{ChatID: 42, LogID: 101, Type: messagetype.Text, Message: "synthetic edit", Extra: "{}"}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("chatId").Int64() != 42 || raw.Lookup("logId").Int64() != 101 || raw.Lookup("type").Int32() != messagetype.Text ||
		raw.Lookup("msg").StringValue() != "synthetic edit" || raw.Lookup("extra").StringValue() != "{}" {
		t.Fatalf("MODIFYMSG = %v", raw)
	}
	for _, bad := range []ModifyRequest{
		{ChatID: 42, LogID: 101, Type: messagetype.Text, Message: "", Extra: "{}"},
		{ChatID: 0, LogID: 101, Type: messagetype.Text, Message: "x", Extra: "{}"},
		{ChatID: 42, LogID: 0, Type: messagetype.Text, Message: "x", Extra: "{}"},
		{ChatID: 42, LogID: 101, Type: messagetype.Text, Message: "x", Extra: "not json"},
	} {
		if _, err := bad.MarshalBSON(); !errors.Is(err, ErrInvalidMessage) && !errors.Is(err, ErrInvalidChatID) && !errors.Is(err, ErrInvalidMessageID) {
			t.Fatalf("%+v accepted: %v", bad, err)
		}
	}
}

func TestDecodeModifyResponseReturnsTheNewRevision(t *testing.T) {
	body, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(102)}, {Key: "type", Value: int32(0)}}},
		{Key: "modifiedChatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "revision", Value: int32(2)}, {Key: "message", Value: "synthetic edit"}}},
	})
	revision, err := DecodeModifyResponse(body, 101)
	if err != nil || revision != 2 {
		t.Fatalf("revision=%d err=%v", revision, err)
	}
	if _, err := DecodeModifyResponse(body, 999); err == nil {
		t.Fatal("response for another message accepted")
	}
}

func TestDeleteRequestNamesOneMessage(t *testing.T) {
	body, err := (DeleteRequest{ChatID: 42, LogID: 101}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("chatId").Int64() != 42 || raw.Lookup("logId").Int64() != 101 {
		t.Fatalf("DELETEMSG = %v", raw)
	}
	elems, _ := raw.Elements()
	if len(elems) != 2 {
		t.Fatalf("DELETEMSG has %d fields", len(elems))
	}
}

// The Mac client keeps a sticker or reply when it edits the text by copying
// chosen keys of the original attachment into MODIFYMSG extra (static
// fixture; eligibility executed).
func TestModifyExtraFollowsTheMacRecipe(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/edits/static-mac-modify-extra.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name        string          `json:"name"`
			Type        int32           `json:"type"`
			Message     string          `json:"message"`
			Attachment  json.RawMessage `json:"attachment"`
			Extra       json.RawMessage `json:"extra"`
			NotEditable bool            `json:"not_editable"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			attachment := ""
			if s := string(c.Attachment); s != `""` {
				attachment = s
			}
			extra, err := ModifyExtra(EditSource{LogID: 101, AuthorID: 2000, Type: c.Type, Message: c.Message, Attachment: attachment})
			if c.NotEditable {
				if !errors.Is(err, ErrNotEditable) {
					t.Fatalf("extra = %s, err = %v; want ErrNotEditable", extra, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal([]byte(extra), &got); err != nil {
				t.Fatalf("extra %q: %v", extra, err)
			}
			if err := json.Unmarshal(c.Extra, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("extra = %s, want %s", extra, c.Extra)
			}
		})
	}
}

func TestDecodeEditSourceReadsTheTargetFromGetMessages(t *testing.T) {
	body, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}, {Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "chatId", Value: int64(42)}, {Key: "authorId", Value: int64(2000)}, {Key: "type", Value: int32(12)},
			{Key: "message", Value: "synthetic caption"}, {Key: "attachment", Value: `{"path":"synthetic/emot_001.png"}`}},
	}}})
	source, err := DecodeEditSource(body, 101)
	if err != nil || source.LogID != 101 || source.AuthorID != 2000 || source.Type != 12 || source.Message != "synthetic caption" || source.Attachment != `{"path":"synthetic/emot_001.png"}` {
		t.Fatalf("source = %+v, err = %v", source, err)
	}
	empty, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if _, err := DecodeEditSource(empty, 101); !errors.Is(err, ErrEditSourceMissing) {
		t.Fatalf("missing target err = %v", err)
	}
	other, _ := bson.Marshal(bson.D{{Key: "chatLogs", Value: bson.A{bson.D{{Key: "logId", Value: int64(999)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "x"}}}}})
	if _, err := DecodeEditSource(other, 101); !errors.Is(err, ErrEditSourceMissing) {
		t.Fatalf("other log err = %v", err)
	}
}
