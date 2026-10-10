package client

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/booking"
	"github.com/frrad/mooo/internal/protocol/loco"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

// Client tests script their LOCO peers with internal/testsupport/loco. These
// helpers only adapt its endpoints to the package's unexported transport.

func newScriptedBackend(t *testing.T, secure bool, steps ...testloco.Step) *testloco.Backend {
	t.Helper()
	backend, err := testloco.NewBackend(secure, steps...)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

// clientWire is the client end of backend as the session's framed transport.
func clientWire(backend *testloco.Backend) *wireConn {
	return &wireConn{c: backend.Endpoint.Client, secure: backend.Endpoint.ClientSecure}
}

func waitBackends(t *testing.T, backends ...*testloco.Backend) {
	t.Helper()
	for _, backend := range backends {
		if err := backend.Wait(2 * time.Second); err != nil {
			t.Fatal(err)
		}
	}
}

// hostDialer dials the scripted backend registered for each host through the
// same adapter OpenWithTestDialers uses.
func hostDialer(hosts testloco.Hosts) wireDialer {
	return testSessionDialers(TestDialers{TLS: hosts.Dial, Secure: hosts.Dial}).secure
}

// scriptedLogin serves booking and checkin and runs carriage on the secure
// carriage connection that checkin names.
func scriptedLogin(t *testing.T, carriage ...testloco.Step) (sessionDialers, []*testloco.Backend) {
	t.Helper()
	bookingBackend := newScriptedBackend(t, false, testloco.Expect("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, testloco.Expect("CHECKIN", nil, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))
	carriageBackend := newScriptedBackend(t, true, carriage...)
	dialers := testSessionDialers(TestDialers{
		TLS:    testloco.Hosts{booking.Host: bookingBackend, "checkin.invalid": checkin}.Dial,
		Secure: testloco.Hosts{"carriage.invalid": carriageBackend}.Dial,
	})
	return dialers, []*testloco.Backend{bookingBackend, checkin, carriageBackend}
}

// scriptedLoginAttempt scripts one LOGINLIST that must carry expectedToken.
// An accepted reply holds the carriage open until the session closes.
func scriptedLoginAttempt(t *testing.T, expectedToken string, loginReply bson.D) (sessionDialers, []*testloco.Backend) {
	t.Helper()
	steps := []testloco.Step{testloco.Expect("LOGINLIST", func(raw bson.Raw) error {
		return requireString(raw, "oauthToken", expectedToken)
	}, loginReply)}
	for _, field := range loginReply {
		if field.Key == "status" {
			if status, ok := field.Value.(int32); ok && (status == 0 || status == -305 || status == -310) {
				steps = append(steps, testloco.HoldOpen())
			}
			break
		}
	}
	return scriptedLogin(t, steps...)
}

// scriptedSession is a bare logged-in session on a secure backend running
// steps, with its read loop started. Tests of one request path use it instead
// of scripting the whole bootstrap.
func scriptedSession(t *testing.T, steps ...testloco.Step) (*session, *testloco.Backend) {
	t.Helper()
	backend := newScriptedBackend(t, true, steps...)
	s := &session{
		wire: clientWire(backend), nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0",
	}
	go s.readLoop()
	return s, backend
}

func mustBSON(document bson.D) []byte {
	body, err := bson.Marshal(document)
	if err != nil {
		panic(err)
	}
	return body
}

func statusDocument(fields ...bson.E) bson.D {
	return append(bson.D{{Key: "status", Value: int32(0)}}, fields...)
}

func requireInt64(raw bson.Raw, key string, want int64) error {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeInt64 || value.Int64() != want {
		return fmt.Errorf("%s is not int64(%d)", key, want)
	}
	return nil
}

func requireString(raw bson.Raw, key, want string) error {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeString || value.StringValue() != want {
		return fmt.Errorf("%s is not %q", key, want)
	}
	return nil
}

func requireExactKeys(raw bson.Raw, want ...string) error {
	elements, err := raw.Elements()
	if err != nil {
		return err
	}
	if len(elements) != len(want) {
		return fmt.Errorf("key count = %d, want %d", len(elements), len(want))
	}
	for index, element := range elements {
		if element.Key() != want[index] {
			return fmt.Errorf("key %d = %q, want %q", index, element.Key(), want[index])
		}
	}
	return nil
}

func syntheticClientJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 0xff, G: 0x40, A: 0xff})
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
