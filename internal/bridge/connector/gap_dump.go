package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/rs/zerolog"

	"github.com/frrad/mooo/internal/client"
)

var errGapDumpDir = errors.New("connector: network.gap_dump_dir must be a directory accessible only by its owner")

// prepareGapDumpDir creates dir with owner-only access when it is missing, and
// otherwise requires that it already has owner-only access.
func prepareGapDumpDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("connector: network.gap_dump_dir: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("connector: network.gap_dump_dir: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errGapDumpDir
	}
	return nil
}

// gapDumpFile is one private debug dump. BodyHex is the raw BSON the gap came
// from and contains private message content.
type gapDumpFile struct {
	ObservedAt string `json:"observed_at"`
	Source     string `json:"source"`
	KakaoType  int32  `json:"kakao_type"`
	GapReason  string `json:"gap_reason"`
	BodySHA256 string `json:"body_sha256"`
	BodyHex    string `json:"body_hex"`
}

var unsafeGapDumpName = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// newGapDumper returns a client.OpenOptions.GapObserver that writes one
// private file per distinct gap body to dir. It logs the file name, never the
// body. An empty dir returns nil, so the dump is off unless configured.
func newGapDumper(dir string, log zerolog.Logger, now func() time.Time) func(client.GapObservation) {
	if dir == "" {
		return nil
	}
	return func(observation client.GapObservation) {
		path, err := writeGapDump(dir, observation, now())
		if err != nil {
			log.Warn().Err(err).Int32("kakao_type", observation.Gap.Type).Msg("Could not write private gap dump")
			return
		}
		log.Warn().Str("dump_file", filepath.Base(path)).Int32("kakao_type", observation.Gap.Type).
			Msg("Wrote private gap dump; it contains message content")
	}
}

func writeGapDump(dir string, observation client.GapObservation, now time.Time) (string, error) {
	sum := sha256.Sum256(observation.Body)
	digest := hex.EncodeToString(sum[:])
	reason := unsafeGapDumpName.ReplaceAllString(observation.Gap.Reason, "_")
	path := filepath.Join(dir, fmt.Sprintf("gap-%s-%s.json", reason, digest[:16]))
	data, err := json.MarshalIndent(gapDumpFile{
		ObservedAt: now.UTC().Format(time.RFC3339),
		Source:     observation.Source,
		KakaoType:  observation.Gap.Type,
		GapReason:  observation.Gap.Reason,
		BodySHA256: digest,
		BodyHex:    hex.EncodeToString(observation.Body),
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
