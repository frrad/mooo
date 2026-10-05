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
	users        map[int64]incomingBlockSyncUser
	chatFavorite map[int64]bool
	memberIDs    []int64
	fullSynced   bool
	revision     int32
}

type incomingBlockSyncUser struct {
	friendType   int32
	blockType    int32
	favorite     bool
	purged       bool
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
		user.blockType = blockType
		user.favorite = false
		user.purged = false
		s.users[id] = user
		s.chatFavorite[user.directChatID] = false
		// Resolved users are updated in place; only unresolved users are
		// represented in the fallback member list.
	}
	return nil
}

func (s *incomingBlockSyncStateContract) complete(isFull bool, revision int32) {
	if isFull {
		s.fullSynced = true
	}
	s.revision = revision
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
	if got := s.users[2]; got.friendType != -3 || got.blockType != 7 || got.favorite || got.purged || s.chatFavorite[20] {
		t.Fatalf("partial-sync effects=%+v chatFavorite=%v", got, s.chatFavorite[20])
	}
	if got, want := s.memberIDs, []int64{99}; !reflect.DeepEqual(got, want) {
		t.Fatalf("member IDs=%v want unresolved fallback %v", got, want)
	}
}

func TestIncomingBlockSyncStateContractRevisionCompletionIsSeparate(t *testing.T) {
	s := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{}, fullSynced: true}
	if err := s.applyPartial([]int64{7}, []int32{3}); err != nil {
		t.Fatal(err)
	}
	if !s.fullSynced || s.revision != 0 {
		t.Fatalf("state completed before completion callback: %+v", s)
	}
	s.complete(false, -2147483648)
	if !s.fullSynced || s.revision != -2147483648 {
		t.Fatalf("partial completion state=%+v", s)
	}
	s.complete(true, 7)
	if !s.fullSynced || s.revision != 7 {
		t.Fatalf("full completion state=%+v", s)
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
