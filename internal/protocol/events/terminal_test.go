package events

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func rawTerminalPacket(method string, body []byte) loco.Packet {
	return loco.Packet{Header: loco.Header{Method: method}, Body: body}
}

func TestDecodeChangeServerIsTypedWithoutPayload(t *testing.T) {
	body, err := bson.Marshal(bson.D{})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(rawTerminalPacket("CHANGESVR", body))
	if err != nil {
		t.Fatalf("Decode(CHANGESVR) error = %v", err)
	}
	change, ok := event.(ChangeServer)
	if !ok {
		t.Fatalf("Decode(CHANGESVR) event = %T, want ChangeServer", event)
	}
	if change.Kind() != KindChangeServer {
		t.Fatalf("CHANGESVR kind = %q, want %q", change.Kind(), KindChangeServer)
	}
}

func TestDecodeKickoutReasonAndAbsentDefault(t *testing.T) {
	withReason, err := bson.Marshal(bson.D{{Key: "reason", Value: int32(10)}})
	if err != nil {
		t.Fatal(err)
	}
	event, err := Decode(rawTerminalPacket("KICKOUT", withReason))
	if err != nil {
		t.Fatalf("Decode(KICKOUT) error = %v", err)
	}
	kickout, ok := event.(Kickout)
	if !ok {
		t.Fatalf("Decode(KICKOUT) event = %T, want Kickout", event)
	}
	if kickout.Reason != 10 || kickout.Kind() != KindKickout {
		t.Fatalf("KICKOUT = %#v, want reason 10 and kind %q", kickout, KindKickout)
	}

	withoutReason, err := bson.Marshal(bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	event, err = Decode(rawTerminalPacket("KICKOUT", withoutReason))
	if err != nil {
		t.Fatalf("Decode(KICKOUT without reason) error = %v", err)
	}
	kickout, ok = event.(Kickout)
	if !ok || kickout.Reason != 0 {
		t.Fatalf("KICKOUT without reason = %#v (%T), want zero reason", event, event)
	}
}

func TestDecodeTerminalPacketsPreservesMalformedAndUnknown(t *testing.T) {
	for _, method := range []string{"CHANGESVR", "KICKOUT"} {
		t.Run(method+" malformed body", func(t *testing.T) {
			event, err := Decode(rawTerminalPacket(method, []byte{0x01, 0x02, 0x03}))
			if event != nil {
				t.Fatalf("malformed %s event = %#v, want nil", method, event)
			}
			if !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("malformed %s error = %v, want ErrMalformedEvent", method, err)
			}
		})
	}

	event, err := Decode(rawTerminalPacket("KICKOUTX", []byte{0x01, 0x02, 0x03}))
	unknown, ok := event.(UnknownPacket)
	if err != nil || !ok || unknown.Method != "KICKOUTX" {
		t.Fatalf("unknown terminal-like method = %#v (%T), error = %v", event, event, err)
	}
}
