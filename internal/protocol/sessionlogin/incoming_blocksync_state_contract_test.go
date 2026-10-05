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
	friendType int32
	blockType  int32
	favorite   bool
	purged     bool
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
		if i >= len(blockTypes) {
			return errIncomingBlockSyncShortTypes
		}
		user, ok := s.users[id]
		if !ok {
			// The source adds a numeric fallback for unresolved users.
			s.memberIDs = append(s.memberIDs, id)
			continue
		}
		user.friendType = -3
		user.blockType = blockTypes[i]
		user.favorite = false
		user.purged = false
		s.users[id] = user
		s.chatFavorite[id] = false
		// Resolved users are updated in place; only unresolved users are
		// represented in the fallback member list.
	}
	return nil
}

func (s *incomingBlockSyncStateContract) complete(isFull bool, revision int32) {
	s.fullSynced = isFull
	s.revision = revision
}

var errIncomingBlockSyncShortTypes = errors.New("BLOCKSYNC block type vector is shorter than block IDs")

func TestIncomingBlockSyncStateContractModelsFullAndPartialEffects(t *testing.T) {
	s := &incomingBlockSyncStateContract{
		users:        map[int64]incomingBlockSyncUser{1: {favorite: true, purged: true}, 2: {favorite: true, purged: true}},
		chatFavorite: map[int64]bool{1: true, 2: true},
	}
	s.applyFull([]int64{1})
	if got := s.users[1]; got.friendType != -4 || got.purged || len(s.memberIDs) != 0 {
		t.Fatalf("full-sync effects=%+v members=%v", got, s.memberIDs)
	}
	if err := s.applyPartial([]int64{2, 99}, []int32{7, 8}); err != nil {
		t.Fatal(err)
	}
	if got := s.users[2]; got.friendType != -3 || got.blockType != 7 || got.favorite || got.purged || s.chatFavorite[2] {
		t.Fatalf("partial-sync effects=%+v chatFavorite=%v", got, s.chatFavorite[2])
	}
	if got, want := s.memberIDs, []int64{99}; !reflect.DeepEqual(got, want) {
		t.Fatalf("member IDs=%v want unresolved fallback %v", got, want)
	}
}

func TestIncomingBlockSyncStateContractRevisionCompletionIsSeparate(t *testing.T) {
	s := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{}}
	if err := s.applyPartial([]int64{7}, []int32{3}); err != nil {
		t.Fatal(err)
	}
	if s.fullSynced || s.revision != 0 {
		t.Fatalf("state completed before completion callback: %+v", s)
	}
	s.complete(false, -2147483648)
	if s.fullSynced || s.revision != -2147483648 {
		t.Fatalf("completion state=%+v", s)
	}
	s.complete(true, 7)
	if !s.fullSynced || s.revision != 7 {
		t.Fatalf("full completion state=%+v", s)
	}
}

func TestIncomingBlockSyncStateContractRejectsShortBlockTypes(t *testing.T) {
	s := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{1: {}}}
	if err := s.applyPartial([]int64{1}, nil); !errors.Is(err, errIncomingBlockSyncShortTypes) {
		t.Fatalf("short block types error=%v", err)
	}
}
