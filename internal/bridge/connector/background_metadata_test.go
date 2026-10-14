package connector

import (
	"context"
	"errors"
	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/client"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestRoomCreationDoesNotWaitForMemberEnrichment(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Enriched later"}}
	response, err := kc.CreateGroup(context.Background(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Portal.MXID != "!selected:test" {
		t.Fatal("room was not bound using source identities")
	}
	for _, call := range backend.metadataCalls {
		if strings.HasPrefix(call, "Members(") {
			t.Fatal("room binding synchronously fetched optional profiles")
		}
	}
	meta := response.Portal.Metadata.(*KakaoPortalMetadata)
	if meta.MembershipPending || len(meta.MembershipRoster) != 3 {
		t.Fatal("unavailable profiles prevented verified membership")
	}
}

// Drain the real worker's steps and the real publication path against a
// framework bridge. This never implements an alternate enrichment algorithm.
func drainMetadataRefresh(t *testing.T, kc *KakaoClient, c kakaoClient) {
	t.Helper()
	kc.mu.Lock()
	kc.client = c
	kc.mu.Unlock()
	for i := 0; i < 100; i++ {
		task, ok, _ := kc.metadataRefresh.take(time.Now())
		if !ok {
			return
		}
		result := kc.performMetadataRefresh(t.Context(), c, task)
		if kc.applyMetadataRefresh(c, result) {
			t.Fatal("unexpected source disconnect while draining metadata")
		}
	}
	t.Fatal("metadata work did not coalesce")
}

func TestBackgroundMetadataEnrichesGhostWithoutMembershipChanges(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Enriched later"}}
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := matrix.GetMembers(t.Context(), response.Portal.MXID)
	if err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	ghost, err := kc.login.Bridge.GetGhostByID(t.Context(), makeUserID(2000))
	if err != nil || ghost.Name != "Enriched later" {
		t.Fatal("background profile was not applied to the existing ghost")
	}
	after, err := matrix.GetMembers(t.Context(), response.Portal.MXID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("profile enrichment changed room membership")
	}
	meta := response.Portal.Metadata.(*KakaoPortalMetadata)
	if meta.MembershipPending || meta.SourceRemoved {
		t.Fatal("enrichment paused source access")
	}
}

func TestBackgroundMetadataLocalTimeoutKeepsSessionAndMembership(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	backend.membersErr = context.DeadlineExceeded
	task, ok, _ := kc.metadataRefresh.take(time.Now())
	if !ok || task.kind != refreshMembers {
		t.Fatal("missing profile task")
	}
	result := kc.performMetadataRefresh(t.Context(), backend, task)
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatal("did not reproduce local request timeout")
	}
	if kc.applyMetadataRefresh(backend, result) || !kc.IsLoggedIn() {
		t.Fatal("local deadline ended the session")
	}
	p, err := kc.store.portalByKey(t.Context(), makePortalKey(5000, kc.login.ID))
	if err != nil || p.Metadata.(*KakaoPortalMetadata).MembershipPending {
		t.Fatal("optional timeout paused membership")
	}
}

func TestBackgroundMetadataRejectsObsoleteAndRemovedRoomResults(t *testing.T) {
	for _, reason := range []string{"new-snapshot", "removed"} {
		t.Run(reason, func(t *testing.T) {
			kc, backend, _ := newGroupCreationFramework(t)
			response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
			if err != nil {
				t.Fatal(err)
			}
			backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Obsolete"}}
			task, ok, _ := kc.metadataRefresh.take(time.Now())
			if !ok {
				t.Fatal("no task")
			}
			result := kc.performMetadataRefresh(t.Context(), backend, task)
			if reason == "new-snapshot" {
				kc.metadataRefresh.remember(task.snapshot)
			} else {
				response.Portal.Metadata.(*KakaoPortalMetadata).SourceRemoved = true
				if err = response.Portal.Save(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if kc.applyMetadataRefresh(backend, result) {
				t.Fatal("optional stale result ended session")
			}
			ghost, err := kc.login.Bridge.GetGhostByID(t.Context(), makeUserID(2000))
			if err != nil || ghost.Name == "Obsolete" {
				t.Fatal("obsolete enrichment was applied")
			}
		})
	}
}

func TestBackgroundMetadataQueueCoalescesAndRotatesContinuations(t *testing.T) {
	q := newMetadataRefreshQueue()
	task := q.remember(chatSnapshot{data: chatmeta.ChatData{ChatID: 3000, Type: "MultiChat"}, roster: []int64{2000, 4000}})
	task.kind, task.ids = refreshMembers, []int64{4000}
	q.offer(task)
	task.ids, task.notBefore = []int64{2000, 4000}, time.Now().Add(time.Minute)
	q.offer(task)
	q.mu.Lock()
	queued := q.pending[task.key()]
	count := len(q.order)
	q.mu.Unlock()
	if count != 1 || !slices.Equal(queued.ids, []int64{4000, 2000}) {
		t.Fatal("retry lost or starved the queued continuation")
	}
	if _, ok, _ := q.take(time.Now()); ok {
		t.Fatal("retry cooldown was bypassed")
	}
}

func TestBackgroundMetadataWorkerStopsAndJoinsBlockedSource(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	blocked := &blockedMetadataClient{kakaoClient: backend, entered: entered}
	_, stop := kc.startMetadataRefresh(blocked)
	select {
	case <-entered:
	case <-time.After(time.Second):
		stop()
		t.Fatal("worker did not enter source request")
	}
	joined := make(chan struct{})
	go func() { stop(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel and join source work")
	}
}

type blockedMetadataClient struct {
	kakaoClient
	entered chan struct{}
}

func (c *blockedMetadataClient) Members(ctx context.Context, chatID int64, ids []int64) ([]chatmeta.Member, error) {
	close(c.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBackgroundMetadataWrongRoomBoardsCannotClearTopic(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Portal.Topic = "Preserved"
	if err = response.Portal.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	backend.moimResponse = chatmeta.MoimResponse{ChatID: 6000}
	selected := kc.metadataRefresh.rooms()[0]
	selected.kind = refreshBoards
	result := kc.performMetadataRefresh(t.Context(), backend, selected)
	if !errors.Is(result.err, errChatInfoMismatch) || result.info != nil {
		t.Fatal("wrong-room response became a topic clear")
	}
	if kc.applyMetadataRefresh(backend, result) {
		t.Fatal("per-room rejection ended source session")
	}
	cache, err := kc.readRoomMetadata(t.Context(), 5000)
	if err != nil || !cache.BoardsAt.IsZero() {
		t.Fatal("wrong-room response was marked fresh")
	}
}

func TestBackgroundMetadataSharesRealSDKAdmission(t *testing.T) {
	statePath := newIntegrationProfile(t)
	store, err := authstate.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InstallCredentials(authstate.Credentials{UserID: 1000, AccessToken: "test-access", AutoLoginMaterial: []byte("synthetic-material")}); err != nil {
		t.Fatal(err)
	}
	var released atomic.Bool
	dialers, backends := scriptedDialers(t,
		testloco.Expect("LOGINLIST", nil, bson.D{{Key: "status", Value: int32(0)}, {Key: "userId", Value: int64(1000)}, {Key: "chatDatas", Value: bson.A{}}, {Key: "eof", Value: true}}),
		testloco.Expect("MEMBER", func(bson.Raw) error {
			if !released.Load() {
				return errors.New("MEMBER bypassed account admission")
			}
			return nil
		}, bson.D{{Key: "status", Value: int32(0)}, {Key: "chatId", Value: int64(5000)}, {Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(2000)}, {Key: "nickName", Value: "Queued profile"}}}}}),
		testloco.HoldOpen(),
	)
	sdk, err := client.OpenWithTestDialers(statePath, nil, dialers, client.OpenOptions{HistoryPacing: client.HistoryPacing{MinimumInterval: 20 * time.Millisecond, ErrorBackoff: 50 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdk.Shutdown(context.Background()) })
	if err = sdk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	kc, _, _ := newGroupCreationFramework(t)
	if _, err = kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	// Keep exactly the member job; other dependency methods have independent
	// coverage. This test's wire peer permits no bypass or duplicate MEMBER.
	task, ok, _ := kc.metadataRefresh.take(time.Now())
	if !ok || task.kind != refreshMembers {
		t.Fatal("missing profile job")
	}
	kc.metadataRefresh.mu.Lock()
	kc.metadataRefresh.pending = map[string]metadataRefreshTask{}
	kc.metadataRefresh.order = nil
	kc.metadataRefresh.mu.Unlock()
	kc.metadataRefresh.offer(task)
	kc.client = sdk
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { released.Store(true); close(release) }) }
	t.Cleanup(unblock)
	heldDone := make(chan error, 1)
	go func() {
		_, err := sdk.AcquireHistoryBytes(t.Context(), "synthetic-held-account-demand", func(context.Context) ([]byte, error) { close(entered); <-release; return []byte("{}"), nil })
		heldDone <- err
	}()
	<-entered
	results, stop := kc.startMetadataRefresh(sdk)
	defer stop()
	select {
	case <-results:
		t.Fatal("profile completed while another acquisition held admission")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	if err = <-heldDone; err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.err != nil || kc.applyMetadataRefresh(sdk, result) {
			t.Fatal("queued profile did not complete after admission released")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("profile remained starved")
	}
	ghost, err := kc.login.Bridge.GetGhostByID(t.Context(), makeUserID(2000))
	if err != nil || ghost.Name != "Queued profile" {
		t.Fatal("real SDK result did not update ghost")
	}
	stop()
	if err = sdk.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, backends)
}

func TestExistingRoomMetadataDemandDoesNotBlockOrResyncRoster(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	backend.metadataCalls = nil
	backend.chatInfoErr = context.DeadlineExceeded
	info, err := kc.GetChatInfo(t.Context(), response.Portal)
	if err != nil || info.Members != nil || len(backend.metadataCalls) != 0 {
		t.Fatal("existing room metadata demand waited for source or replaced membership")
	}
	task, ok, _ := kc.metadataRefresh.take(time.Now())
	if !ok {
		t.Fatal("enrichment demand was not queued")
	}
	if task.kind != refreshMembers {
		t.Fatal("queue lost earlier profile demand")
	}
}

func TestFailedMembershipRefreshRetriesWithBackoffAndUnpauses(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	response, err := kc.CreateGroup(ctx, &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	// A membership notice's refresh fails locally and pauses the group.
	backend.memberListErr = errors.New("synthetic local refresh failure")
	if _, ok := kc.managedMembershipEvent(ctx, backend, 5000, false); ok {
		t.Fatal("failed refresh reported success")
	}
	p, err := kc.login.Bridge.GetPortalByKey(ctx, response.Portal.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Metadata.(*KakaoPortalMetadata).MembershipPending {
		t.Fatal("failed refresh did not pause the group")
	}
	if _, due, _ := kc.metadataRefresh.take(time.Now()); due {
		t.Fatal("membership retry ran without backoff")
	}
	task, due, _ := kc.metadataRefresh.take(time.Now().Add(metadataRetryDelay(1)))
	if !due || task.kind != retryMembership || task.chatID != 5000 {
		t.Fatalf("scheduled retry = %+v (due %t), want one membership retry for 5000", task, due)
	}
	backend.memberListErr = nil
	if kc.applyMetadataRefresh(backend, kc.performMetadataRefresh(ctx, backend, task)) {
		t.Fatal("membership retry ended the session")
	}
	p, err = kc.login.Bridge.GetPortalByKey(ctx, response.Portal.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.(*KakaoPortalMetadata).MembershipPending {
		t.Fatal("membership retry left the group paused after the source roster converged")
	}
}

func TestBackgroundMetadataMatrixNetworkErrorDoesNotEndSession(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	matrixErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic homeserver refused avatar upload")}
	result := metadataRefreshResult{task: metadataRefreshTask{kind: refreshMembers, chatID: 5000}, trace: newProfileRefreshTrace(backend), err: matrixErr}
	if kc.applyMetadataRefresh(backend, result) {
		t.Fatal("a Matrix-side network error ended the Kakao session")
	}
	result.sourceErr = matrixErr
	if !kc.applyMetadataRefresh(backend, result) {
		t.Fatal("a source transport failure did not end the session")
	}
}

func TestBackgroundMetadataRetryBacksOffToBound(t *testing.T) {
	want := []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, delay := range want {
		if got := metadataRetryDelay(i + 1); got != delay {
			t.Fatalf("retry %d delay = %v, want %v", i+1, got, delay)
		}
	}
}
