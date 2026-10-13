package connector

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
)

func TestGapDumpIsOffByDefault(t *testing.T) {
	if newGapDumper("", zerolog.Nop(), time.Now) != nil {
		t.Fatal("empty debug_dump_dir produced a dumper")
	}
}

func TestPrepareDebugDumpDirCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug-dumps")
	if err := prepareDebugDumpDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir = %v, err = %v", info.Mode(), err)
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := prepareDebugDumpDir(open); !errors.Is(err, errDebugDumpDir) {
		t.Fatalf("group-readable dir err = %v", err)
	}
}

// Each distinct gap body is written once, privately and in full, and the log
// line names only the file and the message type.
func TestGapDumperWritesPrivateRawBody(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	now := time.Date(2026, 10, 13, 19, 16, 5, 0, time.UTC)
	dump := newGapDumper(dir, zerolog.New(&logs), func() time.Time { return now })
	body := []byte("synthetic private body")
	observation := client.GapObservation{Source: "MSG", Gap: events.MessageGap{ChatID: 42, LogID: 101, Type: 2, Reason: "checksum_length_64"}, Body: body}
	dump(observation)
	dump(observation)

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dump files = %v, err = %v", entries, err)
	}
	path := filepath.Join(dir, entries[0].Name())
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("dump mode = %v, err = %v", info.Mode(), err)
	}
	var file gapDumpFile
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Source != "MSG" || file.KakaoType != 2 || file.GapReason != "checksum_length_64" || file.ObservedAt != "2026-10-13T19:16:05Z" || file.BodyHex != hex.EncodeToString(body) {
		t.Fatalf("dump = %+v", file)
	}
	if bytes.Contains(logs.Bytes(), body) || bytes.Contains(logs.Bytes(), []byte(hex.EncodeToString(body))) {
		t.Fatalf("log contains the body: %s", logs.String())
	}
	if !bytes.Contains(logs.Bytes(), []byte(entries[0].Name())) {
		t.Fatalf("log does not name the dump file: %s", logs.String())
	}
}

// A media reply that fails to decode is always logged with only its command,
// reason and shape; the raw reply is dumped only when debug_dump_dir is set.
func TestMediaReplyFailureLogsShapeAndDumpsOnlyWhenEnabled(t *testing.T) {
	body, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}, {Key: "k", Value: "synthetic-upload-token"}, {Key: "p", Value: int32(5228)}})
	_, decodeErr := media.DecodeShipResponse(body)
	sendErr := fmt.Errorf("send failed: %w", decodeErr)

	for _, enabled := range []bool{false, true} {
		kc, _ := newTestClient(t, nil)
		var logs bytes.Buffer
		kc.login.Bridge = &bridgev2.Bridge{Log: zerolog.New(&logs)}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if enabled {
			kc.debugDumpDir = dir
		}
		kc.noteMediaReplyFailure(sendErr)

		lines := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n"))
		var first map[string]any
		if err := json.Unmarshal(lines[0], &first); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"level": "warn", "command": "SHIP", "reply_reason": "vh_missing",
			"reply_shape": "{status:int32 k:string p:int32}", "message": "Kakao media reply failed decoding"}
		if len(first) != len(want) {
			t.Fatalf("log fields = %v, want only %v", first, want)
		}
		for key, value := range want {
			if first[key] != value {
				t.Fatalf("log field %s = %v, want %v", key, first[key], value)
			}
		}
		if bytes.Contains(logs.Bytes(), []byte("synthetic-upload-token")) {
			t.Fatalf("log contains the upload token: %s", logs.String())
		}
		entries, _ := os.ReadDir(dir)
		if !enabled {
			if len(entries) != 0 || len(lines) != 1 {
				t.Fatalf("disabled dump wrote %d files and %d log lines", len(entries), len(lines))
			}
			continue
		}
		if len(entries) != 1 || len(lines) != 2 {
			t.Fatalf("enabled dump wrote %d files and %d log lines", len(entries), len(lines))
		}
		var dump replyDumpFile
		data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
		if err := json.Unmarshal(data, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Command != "SHIP" || dump.Reason != "vh_missing" || dump.BodyHex != hex.EncodeToString(body) {
			t.Fatalf("dump = %+v", dump)
		}
	}
}
