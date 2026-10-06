package sessionlogin

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMarshalBSONPreservesWidthsAndOmitsUnsetObjects(t *testing.T) {
	req := LoginListRequest{
		AppVer: "26.8.0", OS: "mac", Lang: "en", DUUID: "invented-device",
		OAuthToken: "synthetic-token", NType: 1, Revision: 2, DType: 3,
		PCST: 4, BG: false, LastTokenID: 5, LBK: 6,
	}
	wire, err := req.MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(wire)
	for _, key := range []string{"sKey", "MCCMNC", "rp"} {
		if value := raw.Lookup(key); value.Type != 0 {
			t.Fatalf("%s unexpectedly encoded as %v", key, value.Type)
		}
	}
	for _, key := range []string{"ntype", "revision", "dtype", "pcst", "lbk"} {
		if value := raw.Lookup(key); value.Type != bson.TypeInt32 {
			t.Fatalf("%s type = %v", key, value.Type)
		}
	}
	if value := raw.Lookup("lastTokenId"); value.Type != bson.TypeInt64 {
		t.Fatalf("lastTokenId type = %v", value.Type)
	}
	for _, key := range []string{"chatIds", "maxIds"} {
		value := raw.Lookup(key)
		if value.Type != bson.TypeArray {
			t.Fatalf("%s type = %v", key, value.Type)
		}
		values, err := value.Array().Values()
		if err != nil || len(values) != 0 {
			t.Fatalf("%s not empty array: %v %v", key, values, err)
		}
	}
}

func TestMarshalBSONArraysUseInt64AndOmitRP(t *testing.T) {
	req := LoginListRequest{
		AppVer: "26.8.0", OS: "mac", Lang: "en", DUUID: "invented-device",
		OAuthToken: "synthetic-token", MCCMNC: "00101",
		ChatIDs: []int64{7}, MaxIDs: []int64{8},
	}
	wire, err := req.MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(wire)
	chat, _ := raw.Lookup("chatIds").Array().Values()
	if len(chat) != 1 || chat[0].Type != bson.TypeInt64 {
		t.Fatalf("chatIds = %#v", chat)
	}
	if value := raw.Lookup("rp"); value.Type != 0 {
		t.Fatalf("rp unexpectedly encoded as %v", value.Type)
	}
}
