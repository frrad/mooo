package shadow

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

	"github.com/frrad/mooo/internal/protocol/loco"
)

// Mode selects what happens when the shadow decoder disagrees with the
// production mongo-driver decoder on an incoming body. Callers always receive
// the production packet; the shadow never changes what they decode.
type Mode int32

const (
	// ModeOff is the zero value: NewObserver returns nil and no packet is
	// decoded twice.
	ModeOff Mode = iota
	ModeLog
	// ModePanic reports and then crashes the process. Use it only where a
	// remote peer crashing the process is acceptable: tests and lab runs.
	ModePanic
)

// ParseMode accepts "" (off), "off", "log" and "panic".
func ParseMode(value string) (Mode, error) {
	switch value {
	case "", "off":
		return ModeOff, nil
	case "log":
		return ModeLog, nil
	case "panic":
		return ModePanic, nil
	}
	return ModeOff, fmt.Errorf("shadow: unknown BSON shadow mode %q", value)
}

// DefaultMaxBodyBytes bounds the doubled decode cost per packet.
const DefaultMaxBodyBytes = 1 << 20

// Config configures one observer.
type Config struct {
	Mode Mode
	// DumpDir, when set, receives one private JSON file per distinct
	// discrepant body. The files contain the raw body and therefore private
	// message content. The directory must exist with no group/other access.
	DumpDir string
	// MaxBodyBytes skips larger bodies; zero means DefaultMaxBodyBytes.
	MaxBodyBytes int
	// Report receives each discrepancy event. Nil logs through slog.
	Report func(Event)
}

// Event is log-safe: it carries no body content or field values.
type Event struct {
	Method        string
	PacketID      uint32
	BodyLen       int
	BodySHA256    string
	Discrepancies []Discrepancy
	// DumpPath names the private dump file, when one was written or already
	// existed for the same body.
	DumpPath string
	DumpErr  error
}

func (e Event) String() string {
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

// NewObserver returns a client.OpenOptions.PacketObserver that compares each
// incoming BSON body with both decoders. It runs on the read loop before
// dispatch and never alters the packet. ModeOff returns nil, so an unconfigured
// client pays nothing.
func NewObserver(config Config) func(loco.Packet) {
	if config.Mode == ModeOff {
		return nil
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return func(packet loco.Packet) { compareAndReport(config, packet) }
}

func compareAndReport(config Config, packet loco.Packet) {
	if packet.Header.BodyType != loco.BodyTypeBSON || len(packet.Body) == 0 || len(packet.Body) > config.MaxBodyBytes {
		return
	}
	report := Compare(packet.Body)
	if len(report.Discrepancies) == 0 {
		return
	}
	sum := sha256.Sum256(packet.Body)
	event := Event{
		Method:        packet.Header.Method,
		PacketID:      packet.Header.PacketID,
		BodyLen:       len(packet.Body),
		BodySHA256:    hex.EncodeToString(sum[:]),
		Discrepancies: report.Discrepancies,
	}
	if config.DumpDir != "" {
		event.DumpPath, event.DumpErr = writeDump(config.DumpDir, packet, event)
	}
	if config.Report != nil {
		config.Report(event)
	} else {
		slog.Warn(event.String())
	}
	if config.Mode == ModePanic {
		panic(event.String())
	}
}

var unsafeDumpName = regexp.MustCompile(`[^A-Za-z0-9_]+`)

var errDumpDir = errors.New("shadow: BSON shadow dump directory must be a directory with no group or other access")

type dumpFile struct {
	Method        string        `json:"method"`
	PacketID      uint32        `json:"packet_id"`
	Status        uint16        `json:"status"`
	BodyType      uint8         `json:"body_type"`
	BodySHA256    string        `json:"body_sha256"`
	BodyHex       string        `json:"body_hex"`
	Discrepancies []Discrepancy `json:"discrepancies"`
}

// writeDump writes one private file per distinct body. An existing
// file for the same body hash is reused rather than rewritten.
func writeDump(dir string, packet loco.Packet, event Event) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errDumpDir
	}
	method := unsafeDumpName.ReplaceAllString(packet.Header.Method, "_")
	path := filepath.Join(dir, fmt.Sprintf("bson-shadow-%s-%s.json", method, event.BodySHA256[:16]))
	data, err := json.MarshalIndent(dumpFile{
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
