package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestDecodeChatLogMatchesFixture(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/group-history/chat-log.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name    string
			ChatID  int64 `json:"chatId"`
			ChatLog json.RawMessage
			Kind    string
			LogID   int64 `json:"logId"`
			Error   string
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			event, err := DecodeChatLog(tc.ChatID, extJSONToBSON(t, tc.ChatLog))
			switch tc.Error {
			case "misdirected":
				if !errors.Is(err, ErrChatLogMisdirected) {
					t.Fatalf("err = %v, want ErrChatLogMisdirected", err)
				}
				return
			case "unidentifiable":
				if !errors.Is(err, ErrUnidentifiableMessage) {
					t.Fatalf("err = %v, want ErrUnidentifiableMessage", err)
				}
				return
			case "":
			default:
				t.Fatalf("unknown expected error %q", tc.Error)
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind := fmt.Sprintf("%T", event); kind != "events."+tc.Kind {
				t.Fatalf("event = %s, want %s", kind, tc.Kind)
			}
			if chatID, logID, ok := MessagePosition(event); !ok || chatID != tc.ChatID || logID != tc.LogID {
				t.Fatalf("position = (%d, %d, %v), want (%d, %d)", chatID, logID, ok, tc.ChatID, tc.LogID)
			}
		})
	}
}
