package chatmeta

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/v2/bson"
	"os"
	"testing"
)

func TestExecutedPersonalReadFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-metadata/personal-read.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Method      string
		RequestJSON string
		Cases       []struct {
			Input struct {
				Name string
				JSON json.RawMessage
			}
			Records []struct {
				ChatID                       int64
				Name, ImageURL, FullImageURL string
			}
		}
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	body, err := (PersonalMetaRequest{}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	elements, err := bson.Raw(body).Elements()
	if err != nil || len(elements) != 0 || f.Method != PersonalMetaCommand || f.RequestJSON != "{}" {
		t.Fatal("personal metadata request differs from executed empty request")
	}
	for _, c := range f.Cases {
		t.Run(c.Input.Name, func(t *testing.T) {
			var document bson.D
			if err := bson.UnmarshalExtJSON(c.Input.JSON, false, &document); err != nil {
				t.Fatal(err)
			}
			encoded, err := bson.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodePersonalMetaResponse(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Rooms) != len(c.Records) {
				t.Fatal("personal room count differs")
			}
			for _, r := range c.Records {
				meta, ok := got.Rooms[r.ChatID]
				if !ok || meta.Name != r.Name || meta.ImageURL != r.ImageURL || meta.FullImageURL != r.FullImageURL {
					t.Fatal("personal fields differ from executed response")
				}
			}
		})
	}
}

func TestPersonalReadRejectsAmbiguousPairing(t *testing.T) {
	for _, doc := range []bson.D{
		{{Key: "chatIds", Value: bson.A{int64(5000)}}, {Key: "metas", Value: bson.A{}}},
		{{Key: "chatIds", Value: bson.A{int64(5000), int64(5000)}}, {Key: "metas", Value: bson.A{bson.D{}, bson.D{}}}},
		{{Key: "chatIds", Value: bson.A{int64(-1)}}, {Key: "metas", Value: bson.A{bson.D{}}}},
		{{Key: "chatIds", Value: bson.A{int64(5000)}}, {Key: "metas", Value: bson.A{nil}}},
		{},
	} {
		body, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodePersonalMetaResponse(body); err == nil {
			t.Fatal("ambiguous personal pairing accepted")
		}
	}
}
