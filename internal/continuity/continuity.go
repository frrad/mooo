// Package continuity stores the private, durable boundary between events that
// an application has committed and events that Kakao may need to replay.
package continuity

import (
	"errors"
	"path/filepath"
	"sort"
	"sync"

	"github.com/frrad/mooo/internal/privatejson"
)

const Version uint32 = 6

const (
	// Version 4 recorded read watermarks after every SYNCMSG, including
	// catch-up pages whose server-side read effect is unproven. Migration
	// discards them; the cost is at most one repeated acknowledgement.
	unprovenReadVersion uint32 = 4
	previousVersion     uint32 = 3
	oldestVersion       uint32 = 2
)

var (
	ErrInvalidPath       = errors.New("continuity: invalid path")
	ErrCorrupt           = errors.New("continuity: corrupt checkpoint")
	ErrVersionMismatch   = errors.New("continuity: unsupported checkpoint version")
	ErrUnsafePermissions = errors.New("continuity: unsafe permissions")
	ErrInvalidCursor     = errors.New("continuity: invalid cursor")
)

// ChatCursor is the highest application-committed message log ID for one chat.
// Entries are persisted in ChatID order so LOGINLIST positional arrays are
// deterministic across processes.
type ChatCursor struct {
	ChatID   int64 `json:"chat_id"`
	MaxLogID int64 `json:"max_log_id"`
}

// DeliveryStart is the first admitted live message in a never-committed chat.
// It is a replay floor, never a login cursor or read acknowledgement.
type DeliveryStart struct {
	ChatID     int64 `json:"chat_id"`
	FirstLogID int64 `json:"first_log_id"`
}

// ChatTarget is the latest server-observed last log for a synchronized chat.
// It is inventory, not an application commit, and is never sent as a maxId by
// LoginCursors unless the same position was explicitly committed.
type ChatTarget struct {
	ChatID   int64 `json:"chat_id"`
	MaxLogID int64 `json:"max_log_id"`
}

// HistoryGap is an inclusive interval that bounded recovery has not resolved.
// It is operational state, not an acknowledgement: neither bound may be sent
// to Kakao as an application-committed cursor.
type HistoryGap struct {
	ChatID    int64 `json:"chat_id"`
	FromLogID int64 `json:"from_log_id"`
	ToLogID   int64 `json:"to_log_id"`
}

// ReadWatermark is the highest position this client explicitly acknowledged
// as read for one chat. It is independent from ChatCursor: receiving or
// processing a message is not the same operation as acknowledging that it was
// read.
type ReadWatermark struct {
	ChatID    int64 `json:"chat_id"`
	Watermark int64 `json:"watermark"`
}

// Checkpoint is deliberately separate from authentication state. Advancing it
// means the application has durably handled the corresponding messages, not
// merely that the network reader observed them.
type Checkpoint struct {
	Version        uint32          `json:"version"`
	CleanShutdown  bool            `json:"clean_shutdown"`
	LastTokenID    int64           `json:"last_token_id"`
	LBK            int32           `json:"lbk"`
	Chats          []ChatCursor    `json:"chats"`
	KnownChats     []ChatTarget    `json:"known_chats"`
	HistoryGaps    []HistoryGap    `json:"history_gaps"`
	ReadWatermarks []ReadWatermark `json:"read_watermarks"`
	DeliveryStarts []DeliveryStart `json:"delivery_starts,omitempty"`
}

func (c Checkpoint) Clone() Checkpoint {
	chats := make([]ChatCursor, len(c.Chats))
	copy(chats, c.Chats)
	c.Chats = chats
	targets := make([]ChatTarget, len(c.KnownChats))
	copy(targets, c.KnownChats)
	c.KnownChats = targets
	gaps := make([]HistoryGap, len(c.HistoryGaps))
	copy(gaps, c.HistoryGaps)
	c.HistoryGaps = gaps
	watermarks := make([]ReadWatermark, len(c.ReadWatermarks))
	copy(watermarks, c.ReadWatermarks)
	c.ReadWatermarks = watermarks
	c.DeliveryStarts = append([]DeliveryStart(nil), c.DeliveryStarts...)
	return c
}

func (c Checkpoint) SyncTargets() []ChatTarget {
	result := make([]ChatTarget, len(c.KnownChats))
	copy(result, c.KnownChats)
	return result
}

// LoginCursors returns the positional chatIds/maxIds pair required by
// LOGINLIST. The returned slices do not alias the checkpoint.
func (c Checkpoint) LoginCursors() (chatIDs, maxIDs []int64) {
	chatIDs = make([]int64, len(c.Chats))
	maxIDs = make([]int64, len(c.Chats))
	for i, cursor := range c.Chats {
		chatIDs[i], maxIDs[i] = cursor.ChatID, cursor.MaxLogID
	}
	return chatIDs, maxIDs
}

// CommittedMax returns the application-committed maximum for chatID, or zero
// when the chat has never been committed.
func (c Checkpoint) CommittedMax(chatID int64) int64 {
	if index, found := searchChat(c.Chats, chatID); found {
		return c.Chats[index].MaxLogID
	}
	return 0
}

// FirstDeliveryStart returns the recorded first live admission for chatID, or
// zero when none is recorded.
func (c Checkpoint) FirstDeliveryStart(chatID int64) int64 {
	if index, found := searchChat(c.DeliveryStarts, chatID); found {
		return c.DeliveryStarts[index].FirstLogID
	}
	return 0
}

// HasGapThrough reports whether chatID has an outstanding gap that starts at
// or before logID.
func (c Checkpoint) HasGapThrough(chatID, logID int64) bool {
	index, found := searchChat(c.HistoryGaps, chatID)
	return found && c.HistoryGaps[index].FromLogID <= logID
}

// Store serializes checkpoint updates and persists them with atomic replacement.
type Store struct {
	path string
	mu   sync.Mutex
	data Checkpoint
}

// Open creates a missing checkpoint or loads an existing one. The containing
// profile directory must already be owner-only.
func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, ErrInvalidPath
	}
	path = filepath.Clean(path)
	if err := translate(privatejson.ValidatePrivateDir(filepath.Dir(path))); err != nil {
		return nil, err
	}
	data, found, err := privatejson.Read[Checkpoint](path)
	if err != nil {
		return nil, translate(err)
	}
	if !found {
		data = Checkpoint{Version: Version, CleanShutdown: true, Chats: []ChatCursor{}, KnownChats: []ChatTarget{}, HistoryGaps: []HistoryGap{}, ReadWatermarks: []ReadWatermark{}}
		if err := translate(privatejson.WriteInitial(path, data)); err != nil {
			return nil, err
		}
		return &Store{path: path, data: data}, nil
	}
	data, migrated, err := migrate(data)
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := translate(privatejson.WriteAtomic(path, data)); err != nil {
			return nil, err
		}
	}
	return &Store{path: path, data: data}, nil
}

func (s *Store) Snapshot() Checkpoint {
	if s == nil {
		return Checkpoint{Version: Version}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Clone()
}

// InstallSession atomically records server-issued login cursors and marks the
// process as owning an active session. A missing server cursor is represented by
// passing nil and leaves the previous value unchanged.
func (s *Store) InstallSession(lastTokenID *int64, lbk *int32, observed []ChatTarget, deleted []int64, replaceInventory bool) error {
	return s.update(func(next *Checkpoint) error {
		if lastTokenID != nil {
			if *lastTokenID < 0 {
				return ErrInvalidCursor
			}
			next.LastTokenID = *lastTokenID
		}
		if lbk != nil {
			if *lbk < 0 {
				return ErrInvalidCursor
			}
			next.LBK = *lbk
		}
		if replaceInventory {
			next.KnownChats = []ChatTarget{}
		}
		for _, chatID := range deleted {
			if chatID <= 0 {
				return ErrInvalidCursor
			}
			removeByChat(&next.KnownChats, chatID)
			removeByChat(&next.Chats, chatID)
			removeByChat(&next.HistoryGaps, chatID)
			removeByChat(&next.ReadWatermarks, chatID)
			removeByChat(&next.DeliveryStarts, chatID)
		}
		for _, target := range observed {
			if target.ChatID <= 0 || target.MaxLogID < 0 {
				return ErrInvalidCursor
			}
			setTarget(&next.KnownChats, target)
		}
		for _, cursor := range next.Chats {
			raiseTarget(&next.KnownChats, ChatTarget(cursor))
		}
		next.CleanShutdown = false
		return nil
	})
}

// RecordGap durably records an unresolved inclusive interval. Repeated or
// overlapping observations for the same chat are conservatively unioned.
func (s *Store) RecordGap(chatID, fromLogID, toLogID int64) error {
	if chatID <= 0 || fromLogID <= 0 || toLogID < fromLogID {
		return ErrInvalidCursor
	}
	return s.update(func(next *Checkpoint) error {
		upsertByChat(&next.HistoryGaps, HistoryGap{ChatID: chatID, FromLogID: fromLogID, ToLogID: toLogID}, func(gap *HistoryGap) bool {
			gap.FromLogID = min(gap.FromLogID, fromLogID)
			gap.ToLogID = max(gap.ToLogID, toLogID)
			return true
		})
		return nil
	})
}

// ResolveGapThrough removes the recovered prefix of a chat's outstanding gap.
// A gap beyond logID is retained.
func (s *Store) ResolveGapThrough(chatID, logID int64) error {
	if chatID <= 0 || logID <= 0 {
		return ErrInvalidCursor
	}
	return s.update(func(next *Checkpoint) error {
		index, found := searchChat(next.HistoryGaps, chatID)
		if !found || logID < next.HistoryGaps[index].FromLogID {
			return nil
		}
		if logID >= next.HistoryGaps[index].ToLogID {
			removeByChat(&next.HistoryGaps, chatID)
			return nil
		}
		next.HistoryGaps[index].FromLogID = logID + 1
		return nil
	})
}

// RecordDeliveryStart persists an admission boundary without committing it.
// Live admission calls it for every message, so an unchanged boundary is not
// rewritten.
func (s *Store) RecordDeliveryStart(chatID, logID int64) error {
	if chatID <= 0 || logID <= 0 {
		return ErrInvalidCursor
	}
	return s.update(func(next *Checkpoint) error {
		if _, committed := searchChat(next.Chats, chatID); committed {
			return errUnchanged
		}
		raised := raiseTarget(&next.KnownChats, ChatTarget{ChatID: chatID, MaxLogID: logID})
		lowered := upsertByChat(&next.DeliveryStarts, DeliveryStart{ChatID: chatID, FirstLogID: logID}, func(start *DeliveryStart) bool {
			if logID >= start.FirstLogID {
				return false
			}
			start.FirstLogID = logID
			return true
		})
		if !lowered && !raised {
			return errUnchanged
		}
		return nil
	})
}

// CommitMessage advances one per-chat maximum only after the application has
// durably handled that message. Recommitting an older/equal ID is an idempotent
// no-op.
func (s *Store) CommitMessage(chatID, logID int64) (bool, error) {
	if chatID <= 0 || logID <= 0 {
		return false, ErrInvalidCursor
	}
	advanced := false
	err := s.update(func(next *Checkpoint) error {
		removeByChat(&next.DeliveryStarts, chatID)
		advanced = upsertByChat(&next.Chats, ChatCursor{ChatID: chatID, MaxLogID: logID}, func(cursor *ChatCursor) bool {
			if logID <= cursor.MaxLogID {
				return false
			}
			cursor.MaxLogID = logID
			return true
		})
		if advanced {
			raiseTarget(&next.KnownChats, ChatTarget{ChatID: chatID, MaxLogID: logID})
		}
		return nil
	})
	return advanced, err
}

// ReadWatermark returns the durable read acknowledgement for one chat. A
// missing or invalid chat ID has no recorded watermark and returns zero.
func (s *Store) ReadWatermark(chatID int64) int64 {
	if s == nil || chatID <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, found := searchChat(s.data.ReadWatermarks, chatID)
	if !found {
		return 0
	}
	return s.data.ReadWatermarks[index].Watermark
}

// CommitReadWatermark advances one chat's durable read acknowledgement.
// Recommitting an older/equal watermark is an idempotent no-op.
func (s *Store) CommitReadWatermark(chatID, watermark int64) (bool, error) {
	if chatID <= 0 || watermark <= 0 {
		return false, ErrInvalidCursor
	}
	advanced := false
	err := s.update(func(next *Checkpoint) error {
		advanced = upsertByChat(&next.ReadWatermarks, ReadWatermark{ChatID: chatID, Watermark: watermark}, func(current *ReadWatermark) bool {
			if watermark <= current.Watermark {
				return false
			}
			current.Watermark = watermark
			return true
		})
		return nil
	})
	return advanced, err
}

// IsCommitted reports whether a message lies at or below the durable resume
// boundary for its chat.
func (s *Store) IsCommitted(chatID, logID int64) bool {
	if s == nil || chatID <= 0 || logID <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, found := searchChat(s.data.Chats, chatID)
	return found && logID <= s.data.Chats[index].MaxLogID
}

func (s *Store) MarkClean() error {
	return s.update(func(next *Checkpoint) error {
		next.CleanShutdown = true
		return nil
	})
}

// errUnchanged lets an update callback skip the atomic rewrite.
var errUnchanged = errors.New("continuity: unchanged")

func (s *Store) update(change func(*Checkpoint) error) error {
	if s == nil || s.path == "" {
		return ErrInvalidPath
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data.Clone()
	if err := change(&next); err != nil {
		if errors.Is(err, errUnchanged) {
			return nil
		}
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	if err := translate(privatejson.WriteAtomic(s.path, next)); err != nil {
		return err
	}
	s.data = next
	return nil
}

func validate(data Checkpoint) error {
	if data.Version != Version {
		return ErrVersionMismatch
	}
	if data.LastTokenID < 0 || data.LBK < 0 || data.Chats == nil || data.KnownChats == nil || data.HistoryGaps == nil || data.ReadWatermarks == nil {
		return ErrCorrupt
	}
	if !sortedByChat(data.DeliveryStarts, func(s DeliveryStart) bool { return s.FirstLogID > 0 }) ||
		!sortedByChat(data.Chats, func(c ChatCursor) bool { return c.MaxLogID > 0 }) ||
		!sortedByChat(data.KnownChats, func(t ChatTarget) bool { return t.MaxLogID >= 0 }) ||
		!sortedByChat(data.HistoryGaps, func(g HistoryGap) bool { return g.FromLogID > 0 && g.ToLogID >= g.FromLogID }) ||
		!sortedByChat(data.ReadWatermarks, func(w ReadWatermark) bool { return w.Watermark > 0 }) {
		return ErrCorrupt
	}
	return nil
}

// sortedByChat reports whether every entry has a positive chat ID strictly
// greater than its predecessor's and satisfies valid.
func sortedByChat[T chatKeyed](items []T, valid func(T) bool) bool {
	var previous int64
	for _, item := range items {
		if item.chatID() <= previous || !valid(item) {
			return false
		}
		previous = item.chatID()
	}
	return true
}

// chatKeyed is implemented by every per-chat checkpoint entry. Each slice of
// such entries is kept strictly sorted by chat ID; validate enforces it.
type chatKeyed interface{ chatID() int64 }

func (c ChatCursor) chatID() int64    { return c.ChatID }
func (s DeliveryStart) chatID() int64 { return s.ChatID }
func (t ChatTarget) chatID() int64    { return t.ChatID }
func (g HistoryGap) chatID() int64    { return g.ChatID }
func (w ReadWatermark) chatID() int64 { return w.ChatID }

// searchChat returns the position of chatID in a sorted slice and whether an
// entry for it is present there.
func searchChat[T chatKeyed](items []T, chatID int64) (int, bool) {
	index := sort.Search(len(items), func(i int) bool { return items[i].chatID() >= chatID })
	return index, index < len(items) && items[index].chatID() == chatID
}

// upsertByChat inserts value in sorted position when its chat has no entry and
// reports true. Otherwise it applies merge to the existing entry and reports
// merge's result.
func upsertByChat[T chatKeyed](items *[]T, value T, merge func(*T) bool) bool {
	index, found := searchChat(*items, value.chatID())
	if found {
		return merge(&(*items)[index])
	}
	*items = append(*items, value)
	copy((*items)[index+1:], (*items)[index:])
	(*items)[index] = value
	return true
}

// removeByChat deletes chatID's entry from a sorted slice, if present.
func removeByChat[T chatKeyed](items *[]T, chatID int64) {
	if index, found := searchChat(*items, chatID); found {
		*items = append((*items)[:index], (*items)[index+1:]...)
	}
}

func setTarget(targets *[]ChatTarget, target ChatTarget) {
	upsertByChat(targets, target, func(current *ChatTarget) bool {
		*current = target
		return true
	})
}

func raiseTarget(targets *[]ChatTarget, target ChatTarget) bool {
	return upsertByChat(targets, target, func(current *ChatTarget) bool {
		if current.MaxLogID >= target.MaxLogID {
			return false
		}
		current.MaxLogID = target.MaxLogID
		return true
	})
}

// migrate upgrades a decoded older checkpoint to Version and validates it.
func migrate(data Checkpoint) (Checkpoint, bool, error) {
	migrated := false
	if data.Version != Version && len(data.DeliveryStarts) != 0 {
		return Checkpoint{}, false, ErrCorrupt
	}
	if data.Version == 5 {
		data.Version = Version
		migrated = true
	}
	if data.Version == unprovenReadVersion {
		if data.ReadWatermarks == nil {
			return Checkpoint{}, false, ErrCorrupt
		}
		data.ReadWatermarks = []ReadWatermark{}
		data.Version = Version
		migrated = true
	}
	if data.Version == oldestVersion || data.Version == previousVersion {
		if data.LastTokenID < 0 || data.LBK < 0 || data.Chats == nil || data.KnownChats == nil {
			return Checkpoint{}, false, ErrCorrupt
		}
		if data.Version == oldestVersion {
			data.HistoryGaps = []HistoryGap{}
		}
		data.ReadWatermarks = []ReadWatermark{}
		data.Version = Version
		migrated = true
	}
	if err := validate(data); err != nil {
		return Checkpoint{}, false, err
	}
	return data, migrated, nil
}

// translate maps privatejson sentinels onto this package's errors. A missing
// directory or a lost O_EXCL race is reported as corruption.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, privatejson.ErrUnsafePermissions):
		return ErrUnsafePermissions
	default:
		return ErrCorrupt
	}
}
