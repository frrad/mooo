package client

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonshadow"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// invalidUTF8Body is {"message": "\xc3("}: mongo-driver keeps the bytes, the
// official decoder drops the value.
func invalidUTF8Body() []byte {
	value := []byte("\xc3(")
	element := []byte{0x02}
	element = append(element, "message"...)
	element = append(element, 0)
	element = binary.LittleEndian.AppendUint32(element, uint32(len(value)+1))
	element = append(element, value...)
	element = append(element, 0)
	body := binary.LittleEndian.AppendUint32(nil, uint32(4+len(element)+1))
	body = append(body, element...)
	return append(body, 0)
}

func withBSONShadow(t *testing.T, config BSONShadowConfig) {
	t.Helper()
	previous := bsonShadowConfig.Load()
	SetBSONShadow(config)
	t.Cleanup(func() { bsonShadowConfig.Store(previous) })
}

func TestBSONShadowDefaultsToPanicUnderTest(t *testing.T) {
	t.Setenv(BSONShadowModeEnv, "")
	withBSONShadow(t, BSONShadowConfig{})
	if mode := currentBSONShadow().Mode; mode != BSONShadowPanic {
		t.Fatalf("default mode under test = %v, want panic", mode)
	}
}

func TestBSONShadowEnvOverridesOnlyDefaultMode(t *testing.T) {
	t.Setenv(BSONShadowModeEnv, "off")
	withBSONShadow(t, BSONShadowConfig{})
	if mode := currentBSONShadow().Mode; mode != BSONShadowOff {
		t.Fatalf("env default mode = %v, want off", mode)
	}
	SetBSONShadow(BSONShadowConfig{Mode: BSONShadowLog})
	if mode := currentBSONShadow().Mode; mode != BSONShadowLog {
		t.Fatalf("explicit mode = %v, want log despite env", mode)
	}
}

func TestBSONShadowLogReportsEventWithoutContent(t *testing.T) {
	var events []BSONShadowEvent
	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowLog, Report: func(event BSONShadowEvent) { events = append(events, event) }})
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", PacketID: 7, BodyType: loco.BodyTypeBSON}, Body: invalidUTF8Body()})
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Method != "MSG" || event.PacketID != 7 || len(event.Discrepancies) != 1 ||
		event.Discrepancies[0].Kind != bsonshadow.KindOfficialDroppedInvalidUTF8 || event.Discrepancies[0].Path != "message" {
		t.Fatalf("event = %+v", event)
	}
	if event.DumpPath != "" || event.DumpErr != nil {
		t.Fatalf("dump without DumpDir: %q %v", event.DumpPath, event.DumpErr)
	}
	if strings.Contains(event.String(), "\xc3(") || strings.Contains(event.String(), hex.EncodeToString([]byte("\xc3("))) {
		t.Fatalf("event string leaks value: %s", event)
	}
}

func TestBSONShadowAgreementReportsNothing(t *testing.T) {
	called := false
	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowPanic, Report: func(BSONShadowEvent) { called = true }})
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}, {Key: "message", Value: "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: body})
	if called {
		t.Fatal("agreeing body was reported")
	}
}

func TestBSONShadowPanicModePanicsAfterReporting(t *testing.T) {
	reported := false
	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowPanic, Report: func(BSONShadowEvent) { reported = true }})
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("panic mode did not panic")
		}
		if !reported {
			t.Fatal("panic mode did not report before panicking")
		}
		if message, _ := recovered.(string); !strings.Contains(message, "official-dropped-invalid-utf8@message") {
			t.Fatalf("panic message = %v", recovered)
		}
	}()
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: invalidUTF8Body()})
}

func TestBSONShadowOffAndSkips(t *testing.T) {
	called := false
	report := func(BSONShadowEvent) { called = true }
	body := invalidUTF8Body()

	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowOff, Report: report})
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: body})

	SetBSONShadow(BSONShadowConfig{Mode: BSONShadowLog, Report: report})
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", BodyType: 1}, Body: body})

	_, _, skippedBefore := BSONShadowStats()
	SetBSONShadow(BSONShadowConfig{Mode: BSONShadowLog, Report: report, MaxBodyBytes: len(body) - 1})
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: body})
	if _, _, skipped := BSONShadowStats(); skipped != skippedBefore+1 {
		t.Fatalf("skipped = %d, want %d", skipped, skippedBefore+1)
	}
	if called {
		t.Fatal("off, non-BSON or oversized body was reported")
	}
}

func TestBSONShadowDumpIsPrivateAndReconstructsBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dumps")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var events []BSONShadowEvent
	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowLog, DumpDir: dir, Report: func(event BSONShadowEvent) { events = append(events, event) }})
	body := invalidUTF8Body()
	packet := loco.Packet{Header: loco.Header{Method: "MSG", PacketID: 9, Status: 0, BodyType: loco.BodyTypeBSON}, Body: body}
	shadowDecode(packet)
	shadowDecode(packet)
	if len(events) != 2 || events[0].DumpErr != nil || events[0].DumpPath == "" || events[1].DumpPath != events[0].DumpPath {
		t.Fatalf("events = %+v", events)
	}
	info, err := os.Stat(events[0].DumpPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("dump mode = %o, want 600", perm)
	}
	raw, err := os.ReadFile(events[0].DumpPath)
	if err != nil {
		t.Fatal(err)
	}
	var dump bsonShadowDump
	if err := json.Unmarshal(raw, &dump); err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(dump.BodyHex)
	if err != nil {
		t.Fatal(err)
	}
	// The dump alone must reproduce the discrepancy in an isolated test.
	report := bsonshadow.Compare(decoded)
	if dump.Method != "MSG" || len(report.Discrepancies) != 1 || report.Discrepancies[0].Kind != bsonshadow.KindOfficialDroppedInvalidUTF8 {
		t.Fatalf("dump = %+v, recompare = %+v", dump, report)
	}
}

func TestBSONShadowRefusesSharedDumpDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dumps")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var events []BSONShadowEvent
	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowLog, DumpDir: dir, Report: func(event BSONShadowEvent) { events = append(events, event) }})
	shadowDecode(loco.Packet{Header: loco.Header{Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: invalidUTF8Body()})
	if len(events) != 1 || events[0].DumpPath != "" || events[0].DumpErr == nil {
		t.Fatalf("events = %+v, want reported dump error and no file", events)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("shared directory received %d files", len(entries))
	}
}

func TestBSONShadowRunsOnWireRead(t *testing.T) {
	var events []BSONShadowEvent
	withBSONShadow(t, BSONShadowConfig{Mode: BSONShadowLog, Report: func(event BSONShadowEvent) { events = append(events, event) }})
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	frame, err := (loco.Packet{Header: loco.Header{PacketID: 3, Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: invalidUTF8Body()}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = serverConn.Write(frame) }()
	packet, err := (&wireConn{c: clientConn}).read()
	if err != nil {
		t.Fatal(err)
	}
	if string(packet.Body) != string(invalidUTF8Body()) {
		t.Fatal("shadow altered the production packet")
	}
	if len(events) != 1 || events[0].PacketID != 3 {
		t.Fatalf("events = %+v, want one for packet 3", events)
	}
}
