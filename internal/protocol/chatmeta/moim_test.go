package chatmeta

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/v2/bson"
	"os"
	"testing"
)

func TestMoimAnnouncementWireModel(t *testing.T) {
	b, _ := bson.Marshal(bson.D{{Key: "c", Value: int64(3000)}, {Key: "ms", Value: bson.A{bson.D{{Key: "t", Value: int32(1)}, {Key: "ur", Value: int64(43)}, {Key: "br", Value: int64(42)}, {Key: "ct", Value: `{"id":"synthetic-post","type":"TEXT","notice":true,"content":"Synthetic announcement"}`}}}}})
	r, e := DecodeMoimResponse(b)
	if e != nil {
		t.Fatal(e)
	}
	if r.ChatID != 3000 || len(r.Metas) != 1 {
		t.Fatalf("response=%+v", r)
	}
	a, e := r.Metas[0].Announcement()
	if e != nil || a.Text != "Synthetic announcement" || !a.Active {
		t.Fatalf("announcement=%+v err=%v", a, e)
	}
	b, e = (MoimRequest{ChatID: 3000}).MarshalBSON()
	if e != nil {
		t.Fatal(e)
	}
	raw := bson.Raw(b)
	if raw.Lookup("c").Int64() != 3000 || raw.Lookup("ts").Array().Index(0).Int32() != 1 {
		t.Fatal("wrong request mapping")
	}
}

func TestMoimInvalidWireFields(t *testing.T) {
	for _, d := range []bson.D{
		{{Key: "c", Value: int64(0)}, {Key: "ms", Value: bson.A{}}},
		{{Key: "c", Value: int64(1)}, {Key: "ms", Value: "bad"}},
		{{Key: "c", Value: int64(1)}, {Key: "ms", Value: bson.A{bson.D{{Key: "t", Value: "1"}, {Key: "ur", Value: int64(1)}}}}},
		{{Key: "c", Value: int64(1)}, {Key: "ms", Value: bson.A{bson.D{{Key: "t", Value: int32(1)}, {Key: "ur", Value: int64(-1)}}}}},
		{{Key: "c", Value: int64(1)}, {Key: "ms", Value: bson.A{bson.D{{Key: "t", Value: int32(1)}, {Key: "ur", Value: int64(1)}, {Key: "ct", Value: 42}}}}},
	} {
		b, err := bson.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeMoimResponse(b); err == nil {
			t.Fatal("malformed metadata accepted")
		}
	}
	if _, err := (MoimRequest{}).MarshalBSON(); err == nil {
		t.Fatal("invalid request accepted")
	}
	if _, err := (MoimMeta{Type: 1, Content: `{"notice":true`}).Announcement(); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestObservedAnnouncementRemovalSnapshot(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/chatmeta/observed-moim-announcement-removal.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Input json.RawMessage }
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var d bson.D
	if err = bson.UnmarshalExtJSON(f.Input, false, &d); err != nil {
		t.Fatal(err)
	}
	b, err = bson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	r, err := DecodeMoimResponse(b)
	if err != nil || r.ChatID != 3000 || len(r.Metas) != 1 {
		t.Fatalf("response=%+v err=%v", r, err)
	}
	a, err := r.Metas[0].Announcement()
	if err != nil || a.Active || a.Text != "" {
		t.Fatalf("removal=%+v err=%v", a, err)
	}
}
