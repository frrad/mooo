package mchatlogs

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestExecutedModels(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/history-startup/model-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Requests []struct {
			Input struct {
				ChatIDs []int64 `json:"chatIds"`
				Sinces  []int64
			}
			Expected struct{ JSON map[string][]int64 }
		}
		Responses []struct {
			Method   string
			Input    map[string]any
			Expected struct {
				LogsNil   bool
				LogsCount *int
				EOF       bool
			}
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Requests {
		request := Request{ChatIDs: tc.Input.ChatIDs, Sinces: tc.Input.Sinces}
		body, err := request.MarshalBSON()
		if len(request.ChatIDs) == 0 || len(request.ChatIDs) != len(request.Sinces) {
			// Deliberate production guard: the executed model accepts these malformed
			// pairs, but the coordinator never sends an empty/mismatched selection.
			if err == nil {
				t.Fatal("unsafe request accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for key, expected := range tc.Expected.JSON {
			values, err := bson.Raw(body).Lookup(key).Array().Values()
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int64, len(values))
			for i, v := range values {
				if v.Type != bson.TypeInt64 {
					t.Fatal("wrong integer width")
				}
				got[i] = v.Int64()
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatal(key, got, expected)
			}
		}
	}
	for _, tc := range fixture.Responses {
		if tc.Method != "MCHATLOGS" {
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
		if got.EOF != tc.Expected.EOF || (got.ChatLogs == nil) != tc.Expected.LogsNil || (tc.Expected.LogsCount != nil && len(got.ChatLogs) != *tc.Expected.LogsCount) {
			t.Fatal("response differs from executed model")
		}
	}
}

func TestGuards(t *testing.T) {
	for _, req := range []Request{{ChatIDs: []int64{2, 1}, Sinces: []int64{0, 0}}, {ChatIDs: []int64{1, 1}, Sinces: []int64{0, 0}}, {ChatIDs: []int64{1}, Sinces: []int64{-1}}} {
		if _, err := req.MarshalBSON(); err == nil {
			t.Fatal("unsafe request accepted")
		}
	}
	for _, logs := range []bson.A{
		{bson.D{{Key: "chatId", Value: int64(2)}, {Key: "logId", Value: int64(10)}}, bson.D{{Key: "chatId", Value: int64(1)}, {Key: "logId", Value: int64(11)}}},
		{bson.D{{Key: "chatId", Value: int64(1)}, {Key: "logId", Value: int64(10)}}, bson.D{{Key: "chatId", Value: int64(1)}, {Key: "logId", Value: int64(10)}}},
		{bson.D{{Key: "logId", Value: int64(10)}}},
	} {
		body, err := bson.Marshal(bson.D{{Key: "chatLogs", Value: logs}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ParseResponse(body); err == nil {
			t.Fatal("unsafe response accepted")
		}
	}
	// IDs are ordered within a room; they need not increase across rooms.
	body, _ := bson.Marshal(bson.D{{Key: "chatLogs", Value: bson.A{bson.D{{Key: "chatId", Value: int64(1)}, {Key: "logId", Value: int64(100)}}, bson.D{{Key: "chatId", Value: int64(2)}, {Key: "logId", Value: int64(10)}}}}})
	if _, err := ParseResponse(body); err != nil {
		t.Fatal(err)
	}
}
