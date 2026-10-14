package connector

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

// Regression: the same member in multiple rooms was fetched again for each
// snapshot, including after the connector was recreated against its stored DB.
func TestMemberProfileCooldownAcrossRoomsAndRestart(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Shared profile"}}
	read := func(kc *KakaoClient, chatID int64) {
		t.Helper()
		backend.chatInfo.ChatData.ChatID = chatID
		p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(chatID, kc.login.ID))
		if err != nil {
			t.Fatal(err)
		}
		info, err := kc.GetChatInfo(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		member := info.Members.MemberMap[makeUserID(2000)]
		if member.UserInfo == nil || member.UserInfo.Name == nil || *member.UserInfo.Name != "Shared profile" {
			t.Fatal("cached member did not project into the second room")
		}
	}
	for chatID := int64(5000); chatID < 5012; chatID++ {
		read(kc, chatID)
	}
	restarted := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), kc.userID, nil)
	restarted.client = backend
	read(restarted, 5012)
	count := 0
	for _, call := range backend.metadataCalls {
		if strings.HasPrefix(call, "Members(") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("MEMBER requests = %d, want one across rooms and restart", count)
	}
}

// Tests below exercise the production resolver/storage. The scripted backend
// records requested IDs; it does not model native profile freshness behavior.
type cooldownBackend struct {
	chatMetaAPI
	mu       sync.Mutex
	requests [][]int64
	fetch    func(context.Context, int64, []int64) ([]chatmeta.Member, error)
}

func (b *cooldownBackend) Members(ctx context.Context, chatID int64, ids []int64) ([]chatmeta.Member, error) {
	b.mu.Lock()
	b.requests = append(b.requests, append([]int64(nil), ids...))
	b.mu.Unlock()
	return b.fetch(ctx, chatID, ids)
}
func (b *cooldownBackend) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.requests) }

func TestMemberProfileCooldownSurvivesDatabaseReopenAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.db")
	br, db := newFrameworkBridge(t, frameworkOpts{path: path})
	kc, _ := newTestClient(t, nil)
	kc.store = newBridgeStore(br.DB)
	now := time.Now().UTC()
	kc.now = func() time.Time { return now }
	backend := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "Old", AccessPermit: "synthetic-private-permit"}}, nil
	}}
	ctx := context.Background()
	if _, err := kc.resolveMemberProfiles(ctx, backend, 3000, []int64{2000}); err != nil {
		t.Fatal(err)
	}
	raw, _, err := kc.store.get(ctx, memberProfileKey(kc.userID, 2000))
	if err != nil || strings.Contains(raw, "synthetic-private-permit") || strings.Contains(raw, "AccessPermit") {
		t.Fatal("checkpoint stored permit or could not be read")
	}
	if err = db.RawDB.Close(); err != nil {
		t.Fatal(err)
	}
	br, _ = newFrameworkBridge(t, frameworkOpts{path: path})
	restarted, _ := newTestClient(t, nil)
	restarted.store = newBridgeStore(br.DB)
	restarted.now = kc.now
	backend.fetch = func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "New"}}, nil
	}
	for i := 0; i < 12; i++ {
		restarted = newKakaoClient(kc.login, newBridgeStore(br.DB), kc.userID, nil)
		restarted.now = kc.now
		now = now.Add(time.Second)
		profiles, err := restarted.resolveMemberProfiles(ctx, backend, 4000, []int64{2000})
		if err != nil || len(profiles) != 1 || profiles[0].Nickname != "Old" || backend.count() != 1 {
			t.Fatal("restart/cache hit refreshed before expiration")
		}
	}
	now = now.Add(memberProfileFreshness)
	profiles, err := restarted.resolveMemberProfiles(ctx, backend, 4000, []int64{2000})
	if err != nil || len(profiles) != 1 || profiles[0].Nickname != "New" || backend.count() != 2 {
		t.Fatal("expiry did not refresh once")
	}
}

func TestMemberProfileCooldownPartialAndMissingResultsSurviveRestart(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	now := time.Now().UTC()
	kc.now = func() time.Time { return now }
	unavailable := errors.New("synthetic second-batch failure")
	backend := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "Retained"}, {UserID: 9999, Nickname: "Unrequested"}}, unavailable
	}}
	ctx := context.Background()
	profiles, err := kc.resolveMemberProfiles(ctx, backend, 3000, []int64{0, 1000, 2000, 2000, 4000})
	if !errors.Is(err, unavailable) || len(profiles) != 1 || profiles[0].UserID != 2000 {
		t.Fatal("partial profiles discarded or foreign result accepted")
	}
	restarted := newKakaoClient(kc.login, kc.store, kc.userID, nil)
	restarted.now = kc.now
	backend.fetch = func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 4000}}, nil
	}
	profiles, err = restarted.resolveMemberProfiles(ctx, backend, 4000, []int64{2000, 4000})
	if err != nil || len(profiles) != 1 || backend.count() != 1 {
		t.Fatal("missing or failed result retried on restart")
	}
	now = now.Add(memberProfileAttemptCooldown)
	profiles, err = restarted.resolveMemberProfiles(ctx, backend, 4000, []int64{2000, 4000})
	if err != nil || len(profiles) != 2 || profiles[1].Nickname != "" || backend.count() != 2 {
		t.Fatal("missing profile not retried or confirmed clear lost")
	}
	if got := backend.requests[1]; len(got) != 1 || got[0] != 4000 {
		t.Fatal("fresh user fetched again with missing user")
	}
}

func TestMemberProfileCooldownConcurrentClaimsAndLateResponse(t *testing.T) {
	kc, source, _ := newGroupCreationFramework(t)
	peer := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), kc.userID, nil)
	now := time.Now().UTC()
	kc.now = func() time.Time { return now }
	peer.now = kc.now
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	backend := &cooldownBackend{chatMetaAPI: source, fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		close(entered)
		<-release
		return []chatmeta.Member{{UserID: 2000, Nickname: "Late"}}, nil
	}}
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { _, err := kc.resolveMemberProfiles(ctx, backend, 3000, []int64{2000}); done <- err }()
	<-entered
	profiles, err := peer.resolveMemberProfiles(ctx, backend, 4000, []int64{2000})
	if err != nil || len(profiles) != 0 || backend.count() != 1 {
		t.Fatal("concurrent room repeated unresolved request")
	}
	// A newer eligible reservation supersedes the slow request. Its completion
	// must remain authoritative when the old response finally arrives.
	now = now.Add(memberProfileAttemptCooldown)
	newer := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "Newer"}}, nil
	}}
	if _, err = peer.resolveMemberProfiles(ctx, newer, 4000, []int64{2000}); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	record, _, err := kc.readMemberProfile(ctx, 2000)
	if err != nil || record.Profile == nil || record.Profile.Nickname != "Newer" {
		t.Fatal("late response overwrote newer refresh")
	}
}

func TestMemberProfileCooldownFailurePreservesStaleProfileAndAccountIsolation(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	now := time.Now().UTC()
	kc.now = func() time.Time { return now }
	ctx := context.Background()
	backend := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "Retained"}}, nil
	}}
	if _, err := kc.resolveMemberProfiles(ctx, backend, 3000, []int64{2000}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(memberProfileFreshness)
	backend.fetch = func(context.Context, int64, []int64) ([]chatmeta.Member, error) { return nil, context.DeadlineExceeded }
	profiles, err := kc.resolveMemberProfiles(ctx, backend, 4000, []int64{2000})
	if !errors.Is(err, context.DeadlineExceeded) || len(profiles) != 1 || profiles[0].Nickname != "Retained" {
		t.Fatal("failed refresh erased stale profile")
	}
	if _, err = kc.resolveMemberProfiles(ctx, backend, 5000, []int64{2000}); err != nil || backend.count() != 2 {
		t.Fatal("failure requeued before cooldown")
	}
	other := newKakaoClient(kc.login, kc.store, 1001, nil)
	other.now = kc.now
	backend.fetch = func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "Different observer"}}, nil
	}
	profiles, err = other.resolveMemberProfiles(ctx, backend, 4000, []int64{2000})
	if err != nil || backend.count() != 3 || profiles[0].Nickname != "Different observer" {
		t.Fatal("observers shared cached identity data")
	}
}

func TestMemberProfileCooldownStorageFailureStopsFetch(t *testing.T) {
	for _, kind := range []string{"corrupt", "unsafe-size", "write-failure"} {
		t.Run(kind, func(t *testing.T) {
			kc, source, _ := newGroupCreationFramework(t)
			ctx := context.Background()
			key := memberProfileKey(kc.userID, 2000)
			if kind == "write-failure" {
				if _, err := kc.login.Bridge.DB.KV.Exec(ctx, `CREATE TRIGGER fail_profile BEFORE INSERT ON kv_store BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				value := "invalid"
				if kind == "unsafe-size" {
					value = strings.Repeat("x", maxMemberProfileRecord+1)
				}
				if err := kc.store.put(ctx, key, value); err != nil {
					t.Fatal(err)
				}
			}
			backend := &cooldownBackend{chatMetaAPI: source, fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
				t.Error("fetch after invalid or unwritable checkpoint")
				return nil, nil
			}}
			if _, err := kc.resolveMemberProfiles(ctx, backend, 3000, []int64{2000}); err == nil || backend.count() != 0 {
				t.Fatal("storage failure did not stop fetch")
			}
		})
	}
}

// Arrange two independent connector owners reading the same absent SQL key
// before either can reserve it. Exactly one may dispatch the shared identity.
type profileClaimBarrier struct {
	store
	arrived chan struct{}
	proceed chan struct{}
}

func (s profileClaimBarrier) get(ctx context.Context, key string) (string, bool, error) {
	raw, found, err := s.store.get(ctx, key)
	if !found && err == nil && strings.HasPrefix(key, "kakao:member-profile:") {
		s.arrived <- struct{}{}
		select {
		case <-s.proceed:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	return raw, found, err
}
func TestMemberProfileCooldownAtomicClaimAcrossConnectors(t *testing.T) {
	kc, _, _ := newGroupCreationFramework(t)
	peer := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), kc.userID, nil)
	arrived, proceed := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(proceed) }) }
	t.Cleanup(unblock)
	kc.store = profileClaimBarrier{store: kc.store, arrived: arrived, proceed: proceed}
	peer.store = profileClaimBarrier{store: peer.store, arrived: arrived, proceed: proceed}
	backend := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		return []chatmeta.Member{{UserID: 2000, Nickname: "Shared"}}, nil
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for _, owner := range []*KakaoClient{kc, peer} {
		go func() { _, err := owner.resolveMemberProfiles(ctx, backend, 3000, []int64{2000}); done <- err }()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("claims did not reach barrier")
		}
	}
	unblock()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if backend.count() != 1 {
		t.Fatal("both SQL claims dispatched the same user")
	}
}

func TestMemberProfileCooldownCanceledPartialResultRemainsDurable(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	backend := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		cancel()
		return []chatmeta.Member{{UserID: 2000, Nickname: "First batch"}}, context.Canceled
	}}
	profiles, err := kc.resolveMemberProfiles(ctx, backend, 3000, []int64{2000, 4000})
	if !errors.Is(err, context.Canceled) || len(profiles) != 1 {
		t.Fatal("completed partial result lost with caller cancellation")
	}
	record, _, err := kc.readMemberProfile(t.Context(), 2000)
	if err != nil || record.Profile == nil || record.Profile.Nickname != "First batch" {
		t.Fatal("partial result was not durably published")
	}
}

func TestMemberProfileCooldownMembershipChangesRetainPartialResult(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	backend := &fakeKakao{members: []chatmeta.Member{{UserID: 2000, Nickname: "Completed profile"}}, membersErr: errors.New("synthetic later batch failed")}
	kc.client = backend
	changes, err := kc.memberChanges(t.Context(), 3000, []events.MemberIdentity{{UserID: 2000}, {UserID: 4000}}, true)
	if err != nil {
		t.Fatal(err)
	}
	known := changes.MemberMap[makeUserID(2000)]
	if known.UserInfo == nil || known.UserInfo.Name == nil || *known.UserInfo.Name != "Completed profile" {
		t.Fatal("membership enrichment discarded completed partial profile")
	}
	if changes.MemberMap[makeUserID(4000)].UserInfo != nil {
		t.Fatal("missing profile was fabricated")
	}
}

func TestMemberProfileCooldownBoundsEncodedProfile(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	backend := &cooldownBackend{fetch: func(context.Context, int64, []int64) ([]chatmeta.Member, error) {
		// JSON escaping makes these otherwise individually bounded fields exceed
		// the stored-record limit. Never publish a record that cannot be read back.
		expanded := strings.Repeat("<", 8192)
		return []chatmeta.Member{{UserID: 2000, Nickname: expanded, ProfileImageURL: expanded, FullProfileImageURL: expanded}}, nil
	}}
	if _, err := kc.resolveMemberProfiles(t.Context(), backend, 3000, []int64{2000}); !errors.Is(err, errProfileCheckpoint) {
		t.Fatal("oversized encoded record was accepted")
	}
	record, _, err := kc.readMemberProfile(t.Context(), 2000)
	if err != nil || record.Profile != nil {
		t.Fatal("oversized result poisoned durable reservation")
	}
}
