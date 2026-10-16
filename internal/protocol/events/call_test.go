package events

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func callPacket(t *testing.T, attachment *string) bson.D {
	t.Helper()
	log := bson.D{
		{Key: "logId", Value: int64(99)},
		{Key: "authorId", Value: int64(7)},
		{Key: "type", Value: messagetype.Mvoip},
		{Key: "sendAt", Value: int32(1700000000)},
	}
	if attachment != nil {
		// The observed message field duplicates the attachment JSON.
		log = append(log, bson.E{Key: "message", Value: *attachment}, bson.E{Key: "attachment", Value: *attachment})
	}
	return bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: log}}
}

func decodeCallPacket(t *testing.T, attachment *string) CallMessage {
	t.Helper()
	e, err := Decode(packet(t, "MSG", callPacket(t, attachment)))
	if err != nil {
		t.Fatal(err)
	}
	call, ok := e.(CallMessage)
	if !ok {
		t.Fatalf("type 51 decoded as %T", e)
	}
	if call.ChatID != 42 || call.LogID != 99 || call.AuthorID != 7 || call.SentAt != 1700000000 {
		t.Fatalf("call identity = %+v", call)
	}
	if chatID, logID, ok := MessagePosition(call); !ok || chatID != 42 || logID != 99 {
		t.Fatal("call record lost its cursor")
	}
	return call
}

// Call records from real owned-account calls decode to their event type and
// duration (research/fixtures/calls/observed-records.json).
func TestObservedCallRecordsDecode(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name             string `json:"name"`
			Attachment       string `json:"attachment"`
			ExpectedEvent    string `json:"expected_event"`
			ExpectedDuration int64  `json:"expected_duration"`
		} `json:"cases"`
	}
	b, err := os.ReadFile("../../../research/fixtures/calls/observed-records.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cases) == 0 {
		t.Fatal("observed call fixture unreadable")
	}
	for _, c := range fixture.Cases {
		call := decodeCallPacket(t, &c.Attachment)
		if call.Event != c.ExpectedEvent || call.Duration != c.ExpectedDuration {
			t.Errorf("%s: event %q duration %d, want %q %d", c.Name, call.Event, call.Duration, c.ExpectedEvent, c.ExpectedDuration)
		}
	}
}

// The official client recognizes exactly the type strings it maps to a
// non-zero value, and reads duration the way its model does
// (research/fixtures/calls/executed-mac-text.json).
func TestExecutedCallAttachmentsDecodeLikeTheMacModel(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name       string  `json:"name"`
			Attachment *string `json:"attachment"`
			Expected   *struct {
				VoipType int   `json:"voip_type"`
				Duration int64 `json:"duration"`
			} `json:"expected"`
		} `json:"cases"`
	}
	b, err := os.ReadFile("../../../research/fixtures/calls/executed-mac-text.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cases) == 0 {
		t.Fatal("executed call fixture unreadable")
	}
	for _, c := range fixture.Cases {
		call := decodeCallPacket(t, c.Attachment)
		if c.Expected == nil {
			// A deliberate deviation: the official process died on this input.
			if call.Event != "" {
				t.Errorf("%s: event %q, want unrecognized", c.Name, call.Event)
			}
			continue
		}
		wantEvent := ""
		if c.Expected.VoipType != 0 {
			var attachment struct{ Type string }
			if err := json.Unmarshal([]byte(*c.Attachment), &attachment); err != nil {
				t.Fatal(err)
			}
			wantEvent = attachment.Type
		}
		if call.Event != wantEvent || call.Duration != c.Expected.Duration {
			t.Errorf("%s: event %q duration %d, want %q %d", c.Name, call.Event, call.Duration, wantEvent, c.Expected.Duration)
		}
	}
}

func TestCallMemberIDsAcceptNumbersAndNumericStrings(t *testing.T) {
	attachment := `{"type":"add","member":[2000,"4000",true,"x",{}]}`
	call := decodeCallPacket(t, &attachment)
	if fmt.Sprint(call.Members) != "[2000 4000]" {
		t.Fatalf("members = %v", call.Members)
	}
}

// An attachment that is not valid UTF-8 is never shown, so it is simply an
// unrecognized call event rather than a delivery gap.
func TestCallWithInvalidUTF8AttachmentIsUnrecognized(t *testing.T) {
	attachment := "{\"type\":\"bye\xff\",\"duration\":5}"
	if call := decodeCallPacket(t, &attachment); call.Event != "" {
		t.Fatalf("event = %q", call.Event)
	}
}

// Call records carry call-server addresses and a call token; none of the
// decoded content may reach logs.
func TestCallMessageStringRedactsContent(t *testing.T) {
	attachment := `{"type":"v_bye","duration":4242,"csIP":"192.0.2.1","cdrToken":"synthetic-token","member":[31337]}`
	call := decodeCallPacket(t, &attachment)
	for _, rendered := range []string{call.String(), call.GoString(), fmt.Sprintf("%v %+v %#v", call, call, call)} {
		for _, secret := range []string{"v_bye", "4242", "192.0.2.1", "synthetic-token", "31337"} {
			if strings.Contains(rendered, secret) {
				t.Fatalf("%q leaks %q", rendered, secret)
			}
		}
	}
}
