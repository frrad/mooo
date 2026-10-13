package connector

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
)

func TestGapDumpIsOffByDefault(t *testing.T) {
	if newGapDumper("", zerolog.Nop(), time.Now) != nil {
		t.Fatal("empty gap_dump_dir produced a dumper")
	}
}

func TestPrepareGapDumpDirCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gap-dumps")
	if err := prepareGapDumpDir(dir); err != nil {
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
	if err := prepareGapDumpDir(open); !errors.Is(err, errGapDumpDir) {
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
