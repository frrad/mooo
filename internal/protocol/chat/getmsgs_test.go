package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type getMessagesFixture struct {
	Requests []struct {
		Name   string
		ChatID int64 `json:"chatId"`
		LogIDs []int64
		Body   json.RawMessage
		Error  bool
	}
	Responses []struct {
		Name   string
		Body   json.RawMessage
		LogIDs []int64
		Absent bool
		Error  bool
	}
}

func getMessagesExtJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var doc bson.D
	if err := bson.UnmarshalExtJSON(raw, true, &doc); err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func loadGetMessagesFixture(t *testing.T) getMessagesFixture {
	t.Helper()
	data, err := os.ReadFile("../../../research/fixtures/edits/getmsgs.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture getMessagesFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestGetMessagesRequestMatchesFixture(t *testing.T) {
	fixture := loadGetMessagesFixture(t)
	if len(fixture.Requests) == 0 {
		t.Fatal("fixture has no requests")
	}
	for _, tc := range fixture.Requests {
		got, err := (GetMessagesRequest{ChatID: tc.ChatID, LogIDs: tc.LogIDs}).MarshalBSON()
		if tc.Error {
			if err == nil {
				t.Errorf("%s: encoded %v, want an error", tc.Name, bson.Raw(got))
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		if want := getMessagesExtJSON(t, tc.Body); !bytes.Equal(got, want) {
			t.Errorf("%s: body = %v, want %v", tc.Name, bson.Raw(got), bson.Raw(want))
		}
	}
}

func TestGetMessagesRequestRejectsMoreThanTheMaximum(t *testing.T) {
	logIDs := make([]int64, MaxGetMessages+1)
	for i := range logIDs {
		logIDs[i] = int64(i + 1)
	}
	if _, err := (GetMessagesRequest{ChatID: 42, LogIDs: logIDs}).MarshalBSON(); !errors.Is(err, ErrInvalidMessageID) {
		t.Fatalf("err = %v", err)
	}
	if _, err := (GetMessagesRequest{ChatID: 42, LogIDs: logIDs[:MaxGetMessages]}).MarshalBSON(); err != nil {
		t.Fatalf("maximum rejected: %v", err)
	}
}

func TestDecodeGetMessagesResponseMatchesFixture(t *testing.T) {
	fixture := loadGetMessagesFixture(t)
	if len(fixture.Responses) == 0 {
		t.Fatal("fixture has no responses")
	}
	for _, tc := range fixture.Responses {
		logs, err := DecodeGetMessagesResponse(getMessagesExtJSON(t, tc.Body))
		if tc.Error {
			if !errors.Is(err, ErrInvalidGetMessagesResp) {
				t.Errorf("%s: err = %v, want ErrInvalidGetMessagesResp", tc.Name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		if (logs == nil) != tc.Absent {
			t.Errorf("%s: logs nil = %v, want %v", tc.Name, logs == nil, tc.Absent)
		}
		if len(logs) != len(tc.LogIDs) {
			t.Fatalf("%s: %d logs, want %d", tc.Name, len(logs), len(tc.LogIDs))
		}
		for i, want := range tc.LogIDs {
			if got, ok := logs[i].Lookup("logId").Int64OK(); !ok || got != want {
				t.Errorf("%s: log %d logId = %d, want %d", tc.Name, i, got, want)
			}
		}
	}
}
