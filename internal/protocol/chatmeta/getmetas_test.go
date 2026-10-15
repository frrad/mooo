package chatmeta

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Mac 26.8.0 LocoGetMetasRequest has one property, cs (int64 array).
func TestGetMetasRequestEncodesChatIDs(t *testing.T) {
	body, err := GetMetasRequest{ChatIDs: []int64{5000, 6000}}.MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		CS []int64 `bson:"cs"`
	}
	if err = bson.Unmarshal(body, &decoded); err != nil || len(decoded.CS) != 2 || decoded.CS[1] != 6000 {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	keys, _ := bson.Raw(body).Elements()
	if len(keys) != 1 {
		t.Fatalf("request keys = %v", keys)
	}
	for _, ids := range [][]int64{nil, {0}, make([]int64, MaxGetMetasBatch+1)} {
		if _, err = (GetMetasRequest{ChatIDs: ids}).MarshalBSON(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("ids %v: err = %v", ids, err)
		}
	}
}

// LocoGetMetasResponse maps mss to chatMetaSets; LocoChatMetaSet maps c to
// chatId and ms to chatMetas.
func TestDecodeGetMetasResponse(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}, {Key: "mss", Value: bson.A{
		bson.D{{Key: "c", Value: int64(5000)}, {Key: "ms", Value: bson.A{bson.D{{Key: "type", Value: int32(3)}, {Key: "revision", Value: int64(7)}, {Key: "content", Value: "Synthetic name"}}}}},
		bson.D{{Key: "c", Value: int64(6000)}, {Key: "ms", Value: bson.A{}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	sets, err := DecodeGetMetasResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[0].ChatID != 5000 || len(sets[0].Metas) != 1 || sets[0].Metas[0].Revision != 7 || sets[1].ChatID != 6000 {
		t.Fatalf("sets = %+v", sets)
	}
	bad, _ := bson.Marshal(bson.D{{Key: "mss", Value: bson.A{bson.D{{Key: "ms", Value: bson.A{}}}}}})
	if _, err = DecodeGetMetasResponse(bad); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("set without chat id: err = %v", err)
	}
}
