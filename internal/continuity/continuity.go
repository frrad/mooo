// Package continuity stores the private, durable boundary between events that
// an application has committed and events that Kakao may need to replay.
package continuity

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
)

const Version uint32 = 4

const (
	previousVersion uint32 = 3
	oldestVersion   uint32 = 2
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

// ReadWatermark is the highest server read position durably observed for one
// chat. It is independent from ChatCursor: receiving/processing a message is
// not the same operation as acknowledging that it was read.
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
	if err := validatePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	data, migrated, err := read(path)
	if errors.Is(err, os.ErrNotExist) {
		data = Checkpoint{Version: Version, CleanShutdown: true, Chats: []ChatCursor{}, KnownChats: []ChatTarget{}, HistoryGaps: []HistoryGap{}, ReadWatermarks: []ReadWatermark{}}
		if err := writeInitial(path, data); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if migrated {
		if err := writeAtomic(path, data); err != nil {
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
			removeTarget(&next.KnownChats, chatID)
			removeCursor(&next.Chats, chatID)
			removeGap(&next.HistoryGaps, chatID)
			removeReadWatermark(&next.ReadWatermarks, chatID)
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
		index := sort.Search(len(next.HistoryGaps), func(i int) bool { return next.HistoryGaps[i].ChatID >= chatID })
		if index < len(next.HistoryGaps) && next.HistoryGaps[index].ChatID == chatID {
			if fromLogID < next.HistoryGaps[index].FromLogID {
				next.HistoryGaps[index].FromLogID = fromLogID
			}
			if toLogID > next.HistoryGaps[index].ToLogID {
				next.HistoryGaps[index].ToLogID = toLogID
			}
			return nil
		}
		next.HistoryGaps = append(next.HistoryGaps, HistoryGap{})
		copy(next.HistoryGaps[index+1:], next.HistoryGaps[index:])
		next.HistoryGaps[index] = HistoryGap{ChatID: chatID, FromLogID: fromLogID, ToLogID: toLogID}
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
		index := sort.Search(len(next.HistoryGaps), func(i int) bool { return next.HistoryGaps[i].ChatID >= chatID })
		if index >= len(next.HistoryGaps) || next.HistoryGaps[index].ChatID != chatID || logID < next.HistoryGaps[index].FromLogID {
			return nil
		}
		if logID >= next.HistoryGaps[index].ToLogID {
			removeGap(&next.HistoryGaps, chatID)
			return nil
		}
		next.HistoryGaps[index].FromLogID = logID + 1
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
		index := sort.Search(len(next.Chats), func(i int) bool { return next.Chats[i].ChatID >= chatID })
		if index < len(next.Chats) && next.Chats[index].ChatID == chatID {
			if logID <= next.Chats[index].MaxLogID {
				return nil
			}
			next.Chats[index].MaxLogID = logID
			raiseTarget(&next.KnownChats, ChatTarget{ChatID: chatID, MaxLogID: logID})
			advanced = true
			return nil
		}
		next.Chats = append(next.Chats, ChatCursor{})
		copy(next.Chats[index+1:], next.Chats[index:])
		next.Chats[index] = ChatCursor{ChatID: chatID, MaxLogID: logID}
		advanced = true
		raiseTarget(&next.KnownChats, ChatTarget{ChatID: chatID, MaxLogID: logID})
		return nil
	})
	return advanced, err
}

// ReadWatermark returns the durable server read position for one chat. A
// missing or invalid chat ID has no recorded watermark and returns zero.
func (s *Store) ReadWatermark(chatID int64) int64 {
	if s == nil || chatID <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := sort.Search(len(s.data.ReadWatermarks), func(i int) bool { return s.data.ReadWatermarks[i].ChatID >= chatID })
	if index >= len(s.data.ReadWatermarks) || s.data.ReadWatermarks[index].ChatID != chatID {
		return 0
	}
	return s.data.ReadWatermarks[index].Watermark
}

// CommitReadWatermark advances one chat's durable server read position.
// Recommitting an older/equal watermark is an idempotent no-op.
func (s *Store) CommitReadWatermark(chatID, watermark int64) (bool, error) {
	if chatID <= 0 || watermark <= 0 {
		return false, ErrInvalidCursor
	}
	advanced := false
	err := s.update(func(next *Checkpoint) error {
		index := sort.Search(len(next.ReadWatermarks), func(i int) bool { return next.ReadWatermarks[i].ChatID >= chatID })
		if index < len(next.ReadWatermarks) && next.ReadWatermarks[index].ChatID == chatID {
			if watermark <= next.ReadWatermarks[index].Watermark {
				return nil
			}
			next.ReadWatermarks[index].Watermark = watermark
			advanced = true
			return nil
		}
		next.ReadWatermarks = append(next.ReadWatermarks, ReadWatermark{})
		copy(next.ReadWatermarks[index+1:], next.ReadWatermarks[index:])
		next.ReadWatermarks[index] = ReadWatermark{ChatID: chatID, Watermark: watermark}
		advanced = true
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
	index := sort.Search(len(s.data.Chats), func(i int) bool { return s.data.Chats[i].ChatID >= chatID })
	return index < len(s.data.Chats) && s.data.Chats[index].ChatID == chatID && logID <= s.data.Chats[index].MaxLogID
}

func (s *Store) MarkClean() error {
	return s.update(func(next *Checkpoint) error {
		next.CleanShutdown = true
		return nil
	})
}

func (s *Store) update(change func(*Checkpoint) error) error {
	if s == nil || s.path == "" {
		return ErrInvalidPath
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data.Clone()
	if err := change(&next); err != nil {
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	if err := writeAtomic(s.path, next); err != nil {
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
	var previous int64
	for i, cursor := range data.Chats {
		if cursor.ChatID <= 0 || cursor.MaxLogID <= 0 || (i > 0 && cursor.ChatID <= previous) {
			return ErrCorrupt
		}
		previous = cursor.ChatID
	}
	previous = 0
	for i, target := range data.KnownChats {
		if target.ChatID <= 0 || target.MaxLogID < 0 || (i > 0 && target.ChatID <= previous) {
			return ErrCorrupt
		}
		previous = target.ChatID
	}
	previous = 0
	for i, gap := range data.HistoryGaps {
		if gap.ChatID <= 0 || gap.FromLogID <= 0 || gap.ToLogID < gap.FromLogID || (i > 0 && gap.ChatID <= previous) {
			return ErrCorrupt
		}
		previous = gap.ChatID
	}
	previous = 0
	for i, watermark := range data.ReadWatermarks {
		if watermark.ChatID <= 0 || watermark.Watermark <= 0 || (i > 0 && watermark.ChatID <= previous) {
			return ErrCorrupt
		}
		previous = watermark.ChatID
	}
	return nil
}

func setTarget(targets *[]ChatTarget, target ChatTarget) {
	index := sort.Search(len(*targets), func(i int) bool { return (*targets)[i].ChatID >= target.ChatID })
	if index < len(*targets) && (*targets)[index].ChatID == target.ChatID {
		(*targets)[index] = target
		return
	}
	*targets = append(*targets, ChatTarget{})
	copy((*targets)[index+1:], (*targets)[index:])
	(*targets)[index] = target
}

func raiseTarget(targets *[]ChatTarget, target ChatTarget) {
	index := sort.Search(len(*targets), func(i int) bool { return (*targets)[i].ChatID >= target.ChatID })
	if index < len(*targets) && (*targets)[index].ChatID == target.ChatID {
		if (*targets)[index].MaxLogID < target.MaxLogID {
			(*targets)[index].MaxLogID = target.MaxLogID
		}
		return
	}
	setTarget(targets, target)
}

func removeTarget(targets *[]ChatTarget, chatID int64) {
	index := sort.Search(len(*targets), func(i int) bool { return (*targets)[i].ChatID >= chatID })
	if index < len(*targets) && (*targets)[index].ChatID == chatID {
		*targets = append((*targets)[:index], (*targets)[index+1:]...)
	}
}

func removeCursor(cursors *[]ChatCursor, chatID int64) {
	index := sort.Search(len(*cursors), func(i int) bool { return (*cursors)[i].ChatID >= chatID })
	if index < len(*cursors) && (*cursors)[index].ChatID == chatID {
		*cursors = append((*cursors)[:index], (*cursors)[index+1:]...)
	}
}

func removeGap(gaps *[]HistoryGap, chatID int64) {
	index := sort.Search(len(*gaps), func(i int) bool { return (*gaps)[i].ChatID >= chatID })
	if index < len(*gaps) && (*gaps)[index].ChatID == chatID {
		*gaps = append((*gaps)[:index], (*gaps)[index+1:]...)
	}
}

func removeReadWatermark(watermarks *[]ReadWatermark, chatID int64) {
	index := sort.Search(len(*watermarks), func(i int) bool { return (*watermarks)[i].ChatID >= chatID })
	if index < len(*watermarks) && (*watermarks)[index].ChatID == chatID {
		*watermarks = append((*watermarks)[:index], (*watermarks)[index+1:]...)
	}
}

func read(path string) (Checkpoint, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Checkpoint{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return Checkpoint{}, false, ErrUnsafePermissions
	}
	f, err := os.Open(path)
	if err != nil {
		return Checkpoint{}, false, ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	var data Checkpoint
	decoder := json.NewDecoder(io.LimitReader(f, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return Checkpoint{}, false, ErrCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Checkpoint{}, false, ErrCorrupt
	}
	migrated := false
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

func validatePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return ErrCorrupt
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return ErrUnsafePermissions
	}
	return nil
}

func writeInitial(path string, data Checkpoint) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrCorrupt
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := json.NewEncoder(f).Encode(data); err != nil || f.Sync() != nil || f.Close() != nil {
		return ErrCorrupt
	}
	ok = true
	return syncDirectory(filepath.Dir(path))
}

func writeAtomic(path string, data Checkpoint) error {
	dir := filepath.Dir(path)
	if err := validatePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".continuity-*")
	if err != nil {
		return ErrCorrupt
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return ErrUnsafePermissions
	}
	if err := json.NewEncoder(tmp).Encode(data); err != nil || tmp.Sync() != nil || tmp.Close() != nil {
		return ErrCorrupt
	}
	if err := os.Rename(name, path); err != nil {
		return ErrCorrupt
	}
	ok = true
	if err := os.Chmod(path, 0o600); err != nil {
		return ErrUnsafePermissions
	}
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return ErrCorrupt
	}
	return nil
}
