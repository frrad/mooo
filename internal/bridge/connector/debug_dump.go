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
	"github.com/frrad/mooo/internal/protocol/media"
)

var errDebugDumpDir = errors.New("connector: network.debug_dump_dir must be a directory accessible only by its owner")

// prepareDebugDumpDir creates dir with owner-only access when it is missing,
// and otherwise requires that it already has owner-only access.
func prepareDebugDumpDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("connector: network.debug_dump_dir: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("connector: network.debug_dump_dir: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errDebugDumpDir
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
	return writePrivateDump(path, gapDumpFile{
		ObservedAt: now.UTC().Format(time.RFC3339),
		Source:     observation.Source,
		KakaoType:  observation.Gap.Type,
		GapReason:  observation.Gap.Reason,
		BodySHA256: digest,
		BodyHex:    hex.EncodeToString(observation.Body),
	})
}

// replyDumpFile is one private dump of a media reply that failed to decode.
// BodyHex can contain an upload token.
type replyDumpFile struct {
	ObservedAt string `json:"observed_at"`
	Command    string `json:"command"`
	Reason     string `json:"reason"`
	Shape      string `json:"shape"`
	BodySHA256 string `json:"body_sha256"`
	BodyHex    string `json:"body_hex"`
}

func writeReplyDump(dir string, reply *media.ResponseError, now time.Time) (string, error) {
	body := reply.RawBody()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	name := fmt.Sprintf("reply-%s-%s-%s.json", unsafeGapDumpName.ReplaceAllString(reply.Command, "_"),
		unsafeGapDumpName.ReplaceAllString(reply.Reason, "_"), digest[:16])
	return writePrivateDump(filepath.Join(dir, name), replyDumpFile{
		ObservedAt: now.UTC().Format(time.RFC3339),
		Command:    reply.Command,
		Reason:     reply.Reason,
		Shape:      reply.Shape,
		BodySHA256: digest,
		BodyHex:    hex.EncodeToString(body),
	})
}

// writePrivateDump writes v as indented JSON to a new 0600 file. An existing
// file for the same name, which embeds the body hash, is kept.
func writePrivateDump(path string, v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
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

// noteMediaReplyFailure records a media reply that failed to decode: always a
// warning through the bridge logger with only the command, reason and reply
// shape, which identify no account and can be quoted in evidence, and, when
// debug_dump_dir is set, a private dump of the raw reply.
func (kc *KakaoClient) noteMediaReplyFailure(err error) {
	var reply *media.ResponseError
	if !errors.As(err, &reply) || kc.login == nil || kc.login.Bridge == nil {
		return
	}
	log := kc.login.Bridge.Log
	log.Warn().Str("command", reply.Command).Str("reply_reason", reply.Reason).Str("reply_shape", reply.Shape).
		Msg("Kakao media reply failed decoding")
	if kc.debugDumpDir == "" {
		return
	}
	path, dumpErr := writeReplyDump(kc.debugDumpDir, reply, kc.now())
	if dumpErr != nil {
		log.Warn().Err(dumpErr).Str("command", reply.Command).Msg("Could not write private reply dump")
		return
	}
	log.Warn().Str("dump_file", filepath.Base(path)).Str("command", reply.Command).
		Msg("Wrote private reply dump; it can contain an upload token")
}
