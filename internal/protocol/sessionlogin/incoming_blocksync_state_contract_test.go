package sessionlogin

import (
	"errors"
	"reflect"
	"testing"
)

// incomingBlockSyncStateContract is a small executable model of the state
// effects recovered from the BLOCKSYNC write block. It is deliberately not a
// datastore or a transport implementation: commit, rollback, retry, and
// worker-error behavior remain untraced source gaps.
type incomingBlockSyncStateContract struct {
	users                  map[int64]incomingBlockSyncUser
	chatFavorite           map[int64]bool
	memberIDs              []int64
	fullSynced             bool
	revision               int32
	revisionUpdates        int
	memberCallbackPending  bool
	memberCallbackCount    int
	memberCallbackFull     bool
	memberCallbackRevision int32
	database               bool
	operationQueue         bool
}

type incomingBlockSyncUser struct {
	friendType   int32
	blockType    int32
	userType     int32
	favorite     bool
	purged       bool
	hidden       bool
	newlyAdded   bool
	directChatID int64
}

func (s *incomingBlockSyncStateContract) applyFull(ids []int64) {
	for _, id := range ids {
		user, ok := s.users[id]
		if !ok {
			continue
		}
		user.friendType = -4
		user.purged = false
		s.users[id] = user
	}
}

func (s *incomingBlockSyncStateContract) applyPartial(ids []int64, blockTypes []int32) error {
	for i, id := range ids {
		user, ok := s.users[id]
		if !ok {
			// The source adds a numeric fallback for unresolved users.
			s.memberIDs = append(s.memberIDs, id)
			continue
		}
		blockType := int32(0)
		if blockTypes != nil {
			if i >= len(blockTypes) {
				return errIncomingBlockSyncTypeIndexUntraced
			}
			blockType = blockTypes[i]
		}
		user.friendType = -3
		if user.userType == 0 {
			user.userType = 1
		}
		user.blockType = blockType
		user.favorite = false
		user.purged = false
		s.users[id] = user
		if user.directChatID != 0 {
			if _, present := s.chatFavorite[user.directChatID]; present {
				s.chatFavorite[user.directChatID] = false
			}
		}
		// Resolved users are updated in place; only unresolved users are
		// represented in the fallback member list.
	}
	return nil
}

// applyUnblock models the separately captured plus-unblock block. Its source
// lookup passes linkID=0 and missing users are released without fallback work.
func (s *incomingBlockSyncStateContract) applyUnblock(ids []int64) {
	for _, id := range ids {
		user, ok := s.users[id]
		if !ok {
			continue
		}
		if user.userType == 0 {
			user.userType = 1
		}
		user.hidden = false
		if !user.newlyAdded {
			user.friendType = -4
		}
		s.users[id] = user
	}
}

func (s *incomingBlockSyncStateContract) complete(isFull bool, revision int32) {
	if len(s.memberIDs) == 0 {
		if isFull {
			s.fullSynced = true
		}
		if !s.database || !s.operationQueue {
			return
		}
		s.revision = revision
		s.revisionUpdates++
		return
	}
	s.memberCallbackPending = true
	s.memberCallbackFull = isFull
	s.memberCallbackRevision = revision
}

func (s *incomingBlockSyncStateContract) finishMemberCallback() {
	if !s.memberCallbackPending {
		return
	}
	s.memberCallbackPending = false
	s.memberCallbackCount++
	if s.memberCallbackFull {
		s.fullSynced = true
	}
	if !s.database || !s.operationQueue {
		return
	}
	s.revision = s.memberCallbackRevision
	s.revisionUpdates++
}

// The source's numberAtIndex: behavior for an allocated short array is not
// yet traced, so the model refuses to guess rather than silently truncate.
var errIncomingBlockSyncTypeIndexUntraced = errors.New("BLOCKSYNC type-index behavior is untraced")

func TestIncomingBlockSyncStateContractModelsFullAndPartialEffects(t *testing.T) {
	s := &incomingBlockSyncStateContract{
		users:        map[int64]incomingBlockSyncUser{1: {favorite: true, purged: true, directChatID: 10}, 2: {favorite: true, purged: true, directChatID: 20}},
		chatFavorite: map[int64]bool{10: true, 20: true},
	}
	s.applyFull([]int64{1})
	if got := s.users[1]; got.friendType != -4 || got.purged || len(s.memberIDs) != 0 {
		t.Fatalf("full-sync effects=%+v members=%v", got, s.memberIDs)
	}
	if err := s.applyPartial([]int64{2, 99}, []int32{7, 8}); err != nil {
		t.Fatal(err)
	}
	if got := s.users[2]; got.friendType != -3 || got.blockType != 7 || got.userType != 1 || got.favorite || got.purged || s.chatFavorite[20] {
		t.Fatalf("partial-sync effects=%+v chatFavorite=%v", got, s.chatFavorite[20])
	}
	if got, want := s.memberIDs, []int64{99}; !reflect.DeepEqual(got, want) {
		t.Fatalf("member IDs=%v want unresolved fallback %v", got, want)
	}
}

func TestIncomingBlockSyncStateContractSkipsMissingChatIdentity(t *testing.T) {
	s := &incomingBlockSyncStateContract{
		users:        map[int64]incomingBlockSyncUser{1: {directChatID: 0}, 2: {directChatID: 22}},
		chatFavorite: map[int64]bool{22: true},
	}
	if err := s.applyPartial([]int64{1, 2}, []int32{3, 4}); err != nil {
		t.Fatal(err)
	}
	if _, present := s.chatFavorite[0]; present {
		t.Fatalf("zero directChatID created chat entry: %v", s.chatFavorite)
	}
	if s.chatFavorite[22] {
		t.Fatalf("resolved chat favorite was not cleared: %v", s.chatFavorite)
	}
}

func TestIncomingBlockSyncStateContractModelsUnblockGuards(t *testing.T) {
	s := &incomingBlockSyncStateContract{
		users:        map[int64]incomingBlockSyncUser{1: {friendType: -3, userType: 0, hidden: true}, 2: {friendType: -3, newlyAdded: true}},
		chatFavorite: map[int64]bool{},
	}
	s.applyUnblock([]int64{1, 2, 99})
	if got := s.users[1]; got.userType != 1 || got.hidden || got.friendType != -4 {
		t.Fatalf("unblock effects=%+v", got)
	}
	if len(s.memberIDs) != 0 {
		t.Fatalf("missing unblock unexpectedly queued work=%v", s.memberIDs)
	}
	if got := s.users[2]; got.friendType != -3 || got.hidden {
		t.Fatalf("newly-added unblock effects=%+v", got)
	}
}

func TestIncomingBlockSyncStateContractRevisionCompletionIsSeparate(t *testing.T) {
	s := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{7: {directChatID: 70}}, chatFavorite: map[int64]bool{}, fullSynced: true, database: true, operationQueue: true}
	if err := s.applyPartial([]int64{7}, []int32{3}); err != nil {
		t.Fatal(err)
	}
	if !s.fullSynced || s.revision != 0 {
		t.Fatalf("state completed before completion callback: %+v", s)
	}
	s.complete(false, -2147483648)
	if !s.fullSynced || s.revision != -2147483648 || s.revisionUpdates != 1 {
		t.Fatalf("partial completion state=%+v", s)
	}
	s.complete(true, 7)
	if !s.fullSynced || s.revision != 7 {
		t.Fatalf("full completion state=%+v", s)
	}
}

func TestIncomingBlockSyncStateContractUsesMemberCallbackBeforeRevision(t *testing.T) {
	s := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{}, chatFavorite: map[int64]bool{}, database: true, operationQueue: true}
	if err := s.applyPartial([]int64{99}, []int32{3}); err != nil {
		t.Fatal(err)
	}
	s.complete(false, 12)
	if !s.memberCallbackPending || s.memberCallbackCount != 0 || s.revisionUpdates != 0 {
		t.Fatalf("nonempty fallback completed too early: %+v", s)
	}
	s.finishMemberCallback()
	if s.memberCallbackPending || s.memberCallbackCount != 1 || s.revision != 12 || s.revisionUpdates != 1 {
		t.Fatalf("member callback completion=%+v", s)
	}
}

func TestIncomingBlockSyncStateContractMissingContextSkipsRevisionOnly(t *testing.T) {
	s := &incomingBlockSyncStateContract{
		users:        map[int64]incomingBlockSyncUser{},
		chatFavorite: map[int64]bool{},
	}
	s.complete(true, 9)
	if !s.fullSynced || s.revision != 0 || s.revisionUpdates != 0 {
		t.Fatalf("empty missing-context completion=%+v", s)
	}
	s.memberIDs = []int64{99}
	s.complete(true, 10)
	if !s.fullSynced || s.revision != 0 || s.revisionUpdates != 0 || !s.memberCallbackPending {
		t.Fatalf("deferred missing-context completion=%+v", s)
	}
	s.finishMemberCallback()
	if !s.fullSynced || s.revision != 0 || s.revisionUpdates != 0 {
		t.Fatalf("missing-context callback completion=%+v", s)
	}
}

func TestIncomingBlockSyncStateContractNilAndEmptyTypesDiffer(t *testing.T) {
	withNil := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{1: {directChatID: 10}}, chatFavorite: map[int64]bool{}}
	if err := withNil.applyPartial([]int64{1}, nil); err != nil || withNil.users[1].blockType != 0 {
		t.Fatalf("nil type array err=%v state=%+v", err, withNil.users[1])
	}
	withEmpty := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{1: {directChatID: 10}}, chatFavorite: map[int64]bool{}}
	if err := withEmpty.applyPartial([]int64{1}, []int32{}); !errors.Is(err, errIncomingBlockSyncTypeIndexUntraced) {
		t.Fatalf("allocated empty type array error=%v", err)
	}
	missing := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{}}
	if err := missing.applyPartial([]int64{99}, []int32{}); err != nil || len(missing.memberIDs) != 1 {
		t.Fatalf("missing user should use fallback without indexing types: err=%v members=%v", err, missing.memberIDs)
	}
}
