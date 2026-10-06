package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frrad/mooo/internal/protocol/bsonshadow"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// BSONShadowMode selects what happens when the shadow decoder disagrees with
// the production mongo-driver decoder on an incoming body. Callers always
// receive the production packet; the shadow never changes what they decode.
type BSONShadowMode int32

const (
	// BSONShadowDefault resolves to $MOOO_BSON_SHADOW when set, otherwise
	// BSONShadowPanic in test binaries and BSONShadowLog elsewhere.
	BSONShadowDefault BSONShadowMode = iota
	BSONShadowOff
	BSONShadowLog
	// BSONShadowPanic reports and then crashes the process. Use it only where
	// a remote peer crashing the process is acceptable: tests and lab runs.
	BSONShadowPanic
)

// ParseBSONShadowMode accepts "", "off", "log" and "panic".
func ParseBSONShadowMode(value string) (BSONShadowMode, error) {
	switch value {
	case "":
		return BSONShadowDefault, nil
	case "off":
		return BSONShadowOff, nil
	case "log":
		return BSONShadowLog, nil
	case "panic":
		return BSONShadowPanic, nil
	}
	return BSONShadowDefault, fmt.Errorf("client: unknown BSON shadow mode %q", value)
}

// DefaultBSONShadowMaxBodyBytes bounds the doubled decode cost per packet.
const DefaultBSONShadowMaxBodyBytes = 1 << 20

// BSONShadowConfig is process-wide; set it once at startup.
type BSONShadowConfig struct {
	Mode BSONShadowMode
	// DumpDir, when set, receives one private JSON file per distinct
	// discrepant body. The files contain the raw body and therefore private
	// message content. The directory must exist with no group/other access.
	DumpDir string
	// MaxBodyBytes skips larger bodies; zero means the default.
	MaxBodyBytes int
	// Report receives each discrepancy event. Nil logs through slog.
	Report func(BSONShadowEvent)
}

// BSONShadowEvent is log-safe: it carries no body content or field values.
type BSONShadowEvent struct {
	Method        string
	PacketID      uint32
	BodyLen       int
	BodySHA256    string
	Discrepancies []bsonshadow.Discrepancy
	// DumpPath names the private dump file, when one was written or already
	// existed for the same body.
	DumpPath string
	DumpErr  error
}

func (e BSONShadowEvent) String() string {
	parts := make([]string, len(e.Discrepancies))
	for i, d := range e.Discrepancies {
		parts[i] = fmt.Sprintf("%s@%s(mongo=%s official=%s)", d.Kind, d.Path, d.Mongo, d.Official)
	}
	out := fmt.Sprintf("bson shadow discrepancy: method=%s packet=%d len=%d sha256=%s [%s]",
		e.Method, e.PacketID, e.BodyLen, e.BodySHA256, strings.Join(parts, " "))
	if e.DumpPath != "" {
		out += " dump=" + e.DumpPath
	}
	if e.DumpErr != nil {
		out += " dump_error=" + e.DumpErr.Error()
	}
	return out
}

var (
	bsonShadowConfig     atomic.Pointer[BSONShadowConfig]
	bsonShadowChecked    atomic.Uint64
	bsonShadowDiscrepant atomic.Uint64
	bsonShadowSkipped    atomic.Uint64
)

// SetBSONShadow replaces the process-wide shadow configuration.
func SetBSONShadow(config BSONShadowConfig) {
	bsonShadowConfig.Store(&config)
}

// BSONShadowStats reports bodies compared, bodies with discrepancies, and
// bodies skipped for exceeding MaxBodyBytes.
func BSONShadowStats() (checked, discrepant, skipped uint64) {
	return bsonShadowChecked.Load(), bsonShadowDiscrepant.Load(), bsonShadowSkipped.Load()
}

func currentBSONShadow() BSONShadowConfig {
	var config BSONShadowConfig
	if stored := bsonShadowConfig.Load(); stored != nil {
		config = *stored
	}
	if config.Mode == BSONShadowDefault {
		config.Mode = defaultBSONShadowMode()
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = DefaultBSONShadowMaxBodyBytes
	}
	return config
}

// BSONShadowModeEnv overrides the default mode (not an explicitly configured
// one) with "off", "log" or "panic".
const BSONShadowModeEnv = "MOOO_BSON_SHADOW"

func defaultBSONShadowMode() BSONShadowMode {
	if mode, err := ParseBSONShadowMode(os.Getenv(BSONShadowModeEnv)); err == nil && mode != BSONShadowDefault {
		return mode
	}
	if testing.Testing() {
		return BSONShadowPanic
	}
	return BSONShadowLog
}

// shadowDecode compares an incoming body with both decoders. It runs on the
// read loop before dispatch and never alters the packet.
func shadowDecode(packet loco.Packet) {
	config := currentBSONShadow()
	if config.Mode == BSONShadowOff || packet.Header.BodyType != loco.BodyTypeBSON || len(packet.Body) == 0 {
		return
	}
	if len(packet.Body) > config.MaxBodyBytes {
		bsonShadowSkipped.Add(1)
		return
	}
	bsonShadowChecked.Add(1)
	report := bsonshadow.Compare(packet.Body)
	if len(report.Discrepancies) == 0 {
		return
	}
	bsonShadowDiscrepant.Add(1)
	sum := sha256.Sum256(packet.Body)
	event := BSONShadowEvent{
		Method:        packet.Header.Method,
		PacketID:      packet.Header.PacketID,
		BodyLen:       len(packet.Body),
		BodySHA256:    hex.EncodeToString(sum[:]),
		Discrepancies: report.Discrepancies,
	}
	if config.DumpDir != "" {
		event.DumpPath, event.DumpErr = writeBSONShadowDump(config.DumpDir, packet, event)
	}
	if config.Report != nil {
		config.Report(event)
	} else {
		slog.Warn(event.String())
	}
	if config.Mode == BSONShadowPanic {
		panic(event.String())
	}
}

var unsafeDumpName = regexp.MustCompile(`[^A-Za-z0-9_]+`)

var errBSONShadowDumpDir = errors.New("client: BSON shadow dump directory must be a directory with no group or other access")

type bsonShadowDump struct {
	Method        string                   `json:"method"`
	PacketID      uint32                   `json:"packet_id"`
	Status        uint16                   `json:"status"`
	BodyType      uint8                    `json:"body_type"`
	BodySHA256    string                   `json:"body_sha256"`
	BodyHex       string                   `json:"body_hex"`
	Discrepancies []bsonshadow.Discrepancy `json:"discrepancies"`
}

// writeBSONShadowDump writes one private file per distinct body. An existing
// file for the same body hash is reused rather than rewritten.
func writeBSONShadowDump(dir string, packet loco.Packet, event BSONShadowEvent) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errBSONShadowDumpDir
	}
	method := unsafeDumpName.ReplaceAllString(packet.Header.Method, "_")
	path := filepath.Join(dir, fmt.Sprintf("bson-shadow-%s-%s.json", method, event.BodySHA256[:16]))
	data, err := json.MarshalIndent(bsonShadowDump{
		Method:        packet.Header.Method,
		PacketID:      packet.Header.PacketID,
		Status:        packet.Header.Status,
		BodyType:      packet.Header.BodyType,
		BodySHA256:    event.BodySHA256,
		BodyHex:       hex.EncodeToString(packet.Body),
		Discrepancies: event.Discrepancies,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return "", err
	}
	return path, file.Close()
}
