package syncmsg

import (
	"encoding/json"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestExecutedSyncMessageModels(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-history/model-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Requests []struct {
			Method string
			Input  struct {
				ChatID int64
				Cur    int64
				Max    int64
				Count  int32 `json:"cnt"`
			}
			JSON string
		}
		Responses []struct {
			ClassName string
			Input     map[string]any
			LogsNil   bool
			LogsCount *int
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Requests {
		if tc.Method != "SYNCMSG" {
			continue
		}
		t.Run(tc.JSON, func(t *testing.T) {
			var expected map[string]int64
			if err := json.Unmarshal([]byte(tc.JSON), &expected); err != nil {
				t.Fatal(err)
			}
			// Native cnt maps to the production request's held-message Count.
			request := Request{ChatID: tc.Input.ChatID, Cur: tc.Input.Cur, Max: tc.Input.Max, Count: tc.Input.Count}
			body, err := request.MarshalBSON()
			if err != nil {
				t.Fatal(err)
			}
			doc := bson.Raw(body)
			for _, key := range []string{"chatId", "cur", "max"} {
				v := doc.Lookup(key)
				if v.Type != bson.TypeInt64 || v.Int64() != expected[key] {
					t.Fatalf("%s differs from executed request", key)
				}
			}
			v := doc.Lookup("cnt")
			if v.Type != bson.TypeInt32 || int64(v.Int32()) != expected["cnt"] {
				t.Fatal("held count differs from executed request")
			}
		})
	}
	for _, tc := range fixture.Responses {
		if tc.ClassName != "LocoSyncMsgResponse" {
			continue
		}
		body, err := bson.Marshal(tc.Input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseResponse(body)
		if err != nil {
			t.Fatal(err)
		}
		if (tc.LogsCount != nil && len(got.ChatLogs) != *tc.LogsCount) || (tc.LogsNil && len(got.ChatLogs) != 0) {
			t.Fatal("empty response differs from executed model and documented nil normalization")
		}
	}
}
