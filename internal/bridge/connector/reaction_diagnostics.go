package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// reactionLookupError is a reaction lookup failure with the stage that failed
// and the responses read before it. It unwraps to the underlying error.
type reactionLookupError struct {
	stage string
	// stored is the stored revision, or -1 before it was read. legacyStored
	// is a Mini change's stored legacy revision, or -1 when not read.
	stored       int64
	legacyStored int64
	members      *reactions.MembersResponse
	mini         *reactions.DetailsResponse
	err          error
}

func (e *reactionLookupError) Error() string { return e.err.Error() }
func (e *reactionLookupError) Unwrap() error { return e.err }

// reactionDumpFile is one private dump of a reaction change that could not
// be applied. The member and mini fields are the raw response dictionaries
// and name the reacting users.
type reactionDumpFile struct {
	ObservedAt     string                     `json:"observed_at"`
	Stage          string                     `json:"stage"`
	Error          string                     `json:"error"`
	Change         events.ReactionChanged     `json:"change"`
	StoredRevision int64                      `json:"stored_revision"`
	LegacyStored   int64                      `json:"legacy_stored_revision"`
	MembersFields  map[string]json.RawMessage `json:"members_fields,omitempty"`
	MiniStatus     *int32                     `json:"mini_status,omitempty"`
	MiniFields     map[string]json.RawMessage `json:"mini_fields,omitempty"`
}

func writeReactionDump(dir string, change events.ReactionChanged, failure *reactionLookupError, now time.Time) (string, error) {
	file := reactionDumpFile{
		Stage:          failure.stage,
		Error:          failure.err.Error(),
		Change:         change,
		StoredRevision: failure.stored,
		LegacyStored:   failure.legacyStored,
	}
	if failure.members != nil {
		file.MembersFields = failure.members.Fields
	}
	if failure.mini != nil {
		file.MiniStatus = &failure.mini.Status
		file.MiniFields = failure.mini.Fields
	}
	// The name hashes everything but the time, so a repeated failure with
	// the same inputs is written once.
	content, err := json.Marshal(file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	file.ObservedAt = now.UTC().Format(time.RFC3339)
	name := fmt.Sprintf("reaction-%s-%s.json", unsafeGapDumpName.ReplaceAllString(failure.stage, "_"), hex.EncodeToString(sum[:])[:16])
	return writePrivateDump(filepath.Join(dir, name), file)
}

// reactionErrorClass names a reaction failure without its text, which for a
// backend error can contain URLs or response bodies.
func reactionErrorClass(err error) string {
	var status *macweb.StatusError
	switch {
	case errors.Is(err, errReactionRevision):
		return "revision"
	case errors.Is(err, errReactionUnsupported):
		return "unsupported"
	case errors.As(err, &status):
		return fmt.Sprintf("status_%d", status.Code)
	case errors.Is(err, reactions.ErrInvalidResponse), errors.Is(err, macweb.ErrInvalidJSON), errors.Is(err, macweb.ErrResponseTooLarge):
		return "invalid_response"
	case errors.Is(err, macweb.ErrTransport):
		return "transport"
	}
	return sourceErrorClass(err)
}

// noteReactionFailure records a reaction change that could not be applied:
// always a warning naming the change, the failed stage and the error class,
// and, when debug_dump_dir is set, a private dump of the change, the error
// text and the responses read for it.
func (kc *KakaoClient) noteReactionFailure(change events.ReactionChanged, err error) {
	failure := &reactionLookupError{stage: "apply", stored: -1, legacyStored: -1, err: err}
	errors.As(err, &failure)
	log := kc.log().Warn().Str("error_class", reactionErrorClass(err)).
		Int64("kakao_chat_id", change.ChatID).Int64("kakao_log_id", change.LogID).
		Int32("metadata_type", change.MetadataType).Int64("revision", change.Revision).
		Str("reaction_stage", failure.stage).Int64("stored_revision", failure.stored)
	if failure.legacyStored >= 0 {
		log = log.Int64("legacy_stored_revision", failure.legacyStored)
	}
	if failure.members != nil {
		log = log.Int64("members_revision", failure.members.Revision)
	}
	log.Msg("Kakao reaction change could not be applied")
	if kc.debugDumpDir == "" {
		return
	}
	path, dumpErr := writeReactionDump(kc.debugDumpDir, change, failure, kc.now())
	if dumpErr != nil {
		kc.log().Warn().Err(dumpErr).Str("reaction_stage", failure.stage).Msg("Could not write private reaction dump")
		return
	}
	kc.log().Warn().Str("dump_file", filepath.Base(path)).Str("reaction_stage", failure.stage).
		Msg("Wrote private reaction dump; it names reacting users")
}
