package sessionlogin

import "testing"

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
		s.memberIDs = append(s.memberIDs, id)
	}
}

func (s *incomingBlockSyncStateContract) applyPartial(ids []int64, blockTypes []int32) {
	for i, id := range ids {
		if i >= len(blockTypes) {
			break
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
		s.memberIDs = append(s.memberIDs, id)
	}
}

func (s *incomingBlockSyncStateContract) complete(revision int32) {
	s.fullSynced = true
	s.revision = revision
}

func TestIncomingBlockSyncStateContractModelsFullAndPartialEffects(t *testing.T) {
	s := &incomingBlockSyncStateContract{
		users:        map[int64]incomingBlockSyncUser{1: {favorite: true, purged: true}, 2: {favorite: true, purged: true}},
		chatFavorite: map[int64]bool{1: true, 2: true},
	}
	s.applyFull([]int64{1})
	if got := s.users[1]; got.friendType != -4 || got.purged || len(s.memberIDs) != 1 {
		t.Fatalf("full-sync effects=%+v members=%v", got, s.memberIDs)
	}
	s.applyPartial([]int64{2, 99}, []int32{7, 8})
	if got := s.users[2]; got.friendType != -3 || got.blockType != 7 || got.favorite || got.purged || s.chatFavorite[2] {
		t.Fatalf("partial-sync effects=%+v chatFavorite=%v", got, s.chatFavorite[2])
	}
	if got, want := s.memberIDs, []int64{1, 2, 99}; len(got) != len(want) || got[2] != want[2] {
		t.Fatalf("member IDs=%v want unresolved fallback %v", got, want)
	}
}

func TestIncomingBlockSyncStateContractRevisionCompletionIsSeparate(t *testing.T) {
	s := &incomingBlockSyncStateContract{users: map[int64]incomingBlockSyncUser{}}
	s.applyPartial([]int64{7}, []int32{3})
	if s.fullSynced || s.revision != 0 {
		t.Fatalf("state completed before completion callback: %+v", s)
	}
	s.complete(-2147483648)
	if !s.fullSynced || s.revision != -2147483648 {
		t.Fatalf("completion state=%+v", s)
	}
}
