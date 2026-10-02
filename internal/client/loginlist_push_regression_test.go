package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/continuity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestConnectSessionPreservesMessagesBeforeLoginListReply exercises the real
// reader/bootstrap handoff: unsolicited MSG packets arrive before the
// correlated LOGINLIST response, and must remain ordered and available after
// bootstrap completes.
func TestConnectSessionPreservesMessagesBeforeLoginListReply(t *testing.T) {
	state := reusableTestState()
	booking := newScriptedBackend(t, false, expectRequest("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, expectRequest("CHECKIN", nil, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))
	keepOpen := make(chan struct{})
	carriage := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.read()
		if err != nil {
			return err
		}
		if request.Header.Method != "LOGINLIST" {
			return fmt.Errorf("method = %q, want LOGINLIST", request.Header.Method)
		}
		for i := 0; i < 70; i++ {
			body := mustBSON(bson.D{
				{Key: "chatId", Value: int64(7)},
				{Key: "chatLog", Value: bson.D{
					{Key: "logId", Value: int64(i + 1)},
					{Key: "message", Value: fmt.Sprintf("synthetic-%02d", i)},
				}},
			})
			if err := writeBackendPacket(server, 0, "MSG", body); err != nil {
				return err
			}
		}
		if err := writeBackendPacket(server, request.Header.PacketID, "LOGINLIST", mustBSON(statusDocument(
			bson.E{Key: "eof", Value: true},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lbk", Value: int32(2)},
		))); err != nil {
			return err
		}
		<-keepOpen
		return nil
	})
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			switch host {
			case bookingHost:
				return booking.client, nil
			case "checkin.invalid":
				return checkin.client, nil
			default:
				return nil, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		secure: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host != "carriage.invalid" {
				return nil, fmt.Errorf("unexpected secure host %q", host)
			}
			return carriage.client, nil
		},
	}
	session, err := connectSessionWithResumeOptions(t.Context(), state, continuity.Checkpoint{Version: continuity.Version}, dialers, pingSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(keepOpen)
		_ = session.Close()
		for _, backend := range []*scriptedBackend{booking, checkin, carriage} {
			backend.wait(t)
		}
	}()

	for want := int64(1); want <= 70; want++ {
		select {
		case packet := <-session.Pushes():
			if packet.Header.Method != "MSG" {
				t.Fatalf("push %d method = %q, want MSG", want, packet.Header.Method)
			}
			body, err := bson.Raw(packet.Body).LookupErr("chatLog")
			if err != nil || body.Type != bson.TypeEmbeddedDocument {
				t.Fatalf("push %d chatLog = %v, err=%v", want, body, err)
			}
			logID, err := body.Document().LookupErr("logId")
			if err != nil || logID.Type != bson.TypeInt64 || logID.Int64() != want {
				t.Fatalf("push %d logId = %v, err=%v", want, logID, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("push %d was not delivered after bootstrap", want)
		}
	}
	select {
	case packet := <-session.Pushes():
		t.Fatalf("unexpected extra packet %q", packet.Header.Method)
	default:
	}
}
