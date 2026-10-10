package sessionlogin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type loginListPageCase struct {
	Name          string
	Body          json.RawMessage
	Error         bool
	Status        int32
	EOF           bool
	Deleted       []int64
	Chats         []struct{ ChatID, MaxLogID int64 }
	LastTokenID   *int64 `json:"lastTokenId"`
	LBK           *int32 `json:"lbk"`
	Next          *struct{ LastTokenID, LastChatID int64 }
	CommitsCursor bool `json:"commitsCursor"`
}

func extJSONToBSON(t *testing.T, raw json.RawMessage) bson.Raw {
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

// The chat target case with ll larger than l.logId is the regression from an
// owned acceptance run on 2026-10-10: l stayed at the edited message while ll
// covered an edit feed made while the bridge was offline, so catch-up never
// reached the edit.
func TestDecodeLoginListPageMatchesFixture(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/session-login/loginlist-pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Cases []loginListPageCase }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			body := extJSONToBSON(t, tc.Body)
			page, err := DecodeLoginListPage(body)
			if tc.Error {
				if !errors.Is(err, ErrMalformedPage) {
					t.Fatalf("error = %v, want ErrMalformedPage", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if page.Status != tc.Status || page.EOF != tc.EOF || page.CommitsCursor() != tc.CommitsCursor {
				t.Fatalf("status=%d eof=%v commits=%v, want %d %v %v", page.Status, page.EOF, page.CommitsCursor(), tc.Status, tc.EOF, tc.CommitsCursor)
			}
			if len(page.DeletedChatIDs) != len(tc.Deleted) {
				t.Fatalf("deleted = %v, want %v", page.DeletedChatIDs, tc.Deleted)
			}
			for i, id := range tc.Deleted {
				if page.DeletedChatIDs[i] != id {
					t.Fatalf("deleted = %v, want %v", page.DeletedChatIDs, tc.Deleted)
				}
			}
			if len(page.Chats) != len(tc.Chats) {
				t.Fatalf("chats = %+v, want %+v", page.Chats, tc.Chats)
			}
			for i, want := range tc.Chats {
				got := page.Chats[i]
				if got.ChatID != want.ChatID || got.MaxLogID != want.MaxLogID {
					t.Fatalf("chat %d = (%d, %d), want (%d, %d)", i, got.ChatID, got.MaxLogID, want.ChatID, want.MaxLogID)
				}
			}
			if !equalPointer(page.LastTokenID, tc.LastTokenID) || !equalPointer(page.LBK, tc.LBK) {
				t.Fatalf("lastTokenId=%v lbk=%v, want %v %v", page.LastTokenID, page.LBK, tc.LastTokenID, tc.LBK)
			}
			switch {
			case tc.Next == nil && page.Next != nil:
				t.Fatalf("next = %+v, want none", *page.Next)
			case tc.Next != nil && (page.Next == nil || page.Next.LastTokenID != tc.Next.LastTokenID || page.Next.LastChatID != tc.Next.LastChatID):
				t.Fatalf("next = %+v, want %+v", page.Next, *tc.Next)
			}
		})
	}
}

func equalPointer[T comparable](got, want *T) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func TestLoginListPageChatDataIsAnIndependentCopy(t *testing.T) {
	body := extJSONToBSON(t, json.RawMessage(`{"status": {"$numberInt": "0"}, "eof": true, "chatDatas": [{"c": {"$numberLong": "42"}, "t": "DirectChat"}]}`))
	page, err := DecodeLoginListPage(body)
	if err != nil {
		t.Fatal(err)
	}
	want := append(bson.Raw(nil), page.Chats[0].Data...)
	for i := range body {
		body[i] = 0
	}
	if !bytes.Equal(page.Chats[0].Data, want) {
		t.Fatal("chat data aliases the page body")
	}
	if typ, ok := page.Chats[0].Data.Lookup("t").StringValueOK(); !ok || typ != "DirectChat" {
		t.Fatalf("chat data = %v", page.Chats[0].Data)
	}
}

func TestLChatListRequestFieldOrderAndWidths(t *testing.T) {
	got, err := (LChatListRequest{LastTokenID: 11, LastChatID: 42}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	want, err := bson.Marshal(bson.D{{Key: "lastTokenId", Value: int64(11)}, {Key: "lastChatId", Value: int64(42)}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body = %v, want %v", bson.Raw(got), bson.Raw(want))
	}
}

func TestAcceptedLChatListStatus(t *testing.T) {
	for status, want := range map[int32]bool{0: true, StatusPartial: true, StatusLoginListDeltaOnly: false, -445: false, 1: false} {
		if got := AcceptedLChatListStatus(status); got != want {
			t.Errorf("AcceptedLChatListStatus(%d) = %v, want %v", status, got, want)
		}
	}
}
