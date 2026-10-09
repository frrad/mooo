package connector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type groupCreationMatrix struct {
	bridgev2.MatrixConnector
	mu       sync.Mutex
	members  map[id.UserID]*event.MemberEventContent
	name     string
	failName bool
}

func (m *groupCreationMatrix) Init(*bridgev2.Bridge) {}
func (m *groupCreationMatrix) BotIntent() bridgev2.MatrixAPI {
	return &groupCreationIntent{m: m, mxid: "@bot:test"}
}
func (m *groupCreationMatrix) GhostIntent(user networkid.UserID) bridgev2.MatrixAPI {
	return &groupCreationIntent{m: m, mxid: id.UserID("@kakao_" + user + ":test")}
}
func (m *groupCreationMatrix) NewUserIntent(context.Context, id.UserID, string) (bridgev2.MatrixAPI, string, error) {
	return nil, "", nil
}
func (m *groupCreationMatrix) GetMembers(context.Context, id.RoomID) (map[id.UserID]*event.MemberEventContent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[id.UserID]*event.MemberEventContent{}
	for k, v := range m.members {
		copy := *v
		out[k] = &copy
	}
	return out, nil
}
func (m *groupCreationMatrix) GetPowerLevels(context.Context, id.RoomID) (*event.PowerLevelsEventContent, error) {
	return &event.PowerLevelsEventContent{Users: map[id.UserID]int{"@owner:test": 100, "@bot:test": 100}}, nil
}
func (m *groupCreationMatrix) GetStateEvent(ctx context.Context, room id.RoomID, typ event.Type, key string) (*event.Event, error) {
	e := &event.Event{Type: typ, Sender: "@owner:test"}
	switch typ {
	case event.StatePowerLevels:
		e.Content.Parsed, _ = m.GetPowerLevels(ctx, room)
	case event.StateCreate:
		e.Content.Parsed = &event.CreateEventContent{RoomVersion: "11"}
	default:
		return nil, errors.New("synthetic unsupported state")
	}
	return e, nil
}

type groupCreationIntent struct {
	bridgev2.MatrixAPI
	m    *groupCreationMatrix
	mxid id.UserID
}

func (i *groupCreationIntent) GetMXID() id.UserID   { return i.mxid }
func (i *groupCreationIntent) IsDoublePuppet() bool { return false }
func (i *groupCreationIntent) EnsureJoined(context.Context, id.RoomID, ...bridgev2.EnsureJoinedParams) error {
	i.m.mu.Lock()
	defer i.m.mu.Unlock()
	i.m.members[i.mxid] = &event.MemberEventContent{Membership: event.MembershipJoin}
	return nil
}
func (i *groupCreationIntent) EnsureInvited(_ context.Context, _ id.RoomID, user id.UserID) error {
	i.m.mu.Lock()
	defer i.m.mu.Unlock()
	if current := i.m.members[user]; current == nil || current.Membership != event.MembershipJoin {
		i.m.members[user] = &event.MemberEventContent{Membership: event.MembershipInvite}
	}
	return nil
}
func (i *groupCreationIntent) SendState(_ context.Context, _ id.RoomID, typ event.Type, key string, content *event.Content, _ time.Time) (*mautrix.RespSendEvent, error) {
	i.m.mu.Lock()
	defer i.m.mu.Unlock()
	switch typ {
	case event.StateRoomName:
		if i.m.failName {
			return nil, errors.New("synthetic name failure")
		}
		i.m.name = content.Parsed.(*event.RoomNameEventContent).Name
	case event.StateMember:
		member := *content.Parsed.(*event.MemberEventContent)
		i.m.members[id.UserID(key)] = &member
	}
	return &mautrix.RespSendEvent{EventID: "$state:test"}, nil
}
func (i *groupCreationIntent) SetDisplayName(context.Context, string) error            { return nil }
func (i *groupCreationIntent) SetAvatarURL(context.Context, id.ContentURIString) error { return nil }

func newGroupCreationFramework(t *testing.T) (*KakaoClient, *countingGroupCreateClient, *groupCreationMatrix) {
	t.Helper()
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	matrix := &groupCreationMatrix{members: map[id.UserID]*event.MemberEventContent{
		"@owner:test": {Membership: event.MembershipJoin}, "@bot:test": {Membership: event.MembershipJoin}, "@kakao_2000:test": {Membership: event.MembershipInvite}, "@kakao_4000:test": {Membership: event.MembershipInvite},
	}}
	br, err := newFrameworkBridge(ctx, raw, matrix)
	if err != nil {
		t.Fatal(err)
	}
	user, err := br.GetUserByMXID(ctx, "@owner:test")
	if err != nil {
		t.Fatal(err)
	}
	record := &database.UserLogin{BridgeID: br.ID, UserMXID: user.MXID, ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{Profile: "synthetic"}}
	if err = br.DB.UserLogin.Insert(ctx, record); err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: record, Bridge: br, User: user, Log: zerolog.Nop()}
	kc := newKakaoClient(login, testSelfID, nil)
	login.Client = kc
	backend := &countingGroupCreateClient{fakeKakao: &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: 5000, Type: "MultiChat", ActiveMemberCount: 3, ChatMetas: []chatmeta.ChatMeta{{Type: 3, Content: "Synthetic created group"}}}}, memberList: chatmeta.MemberListResponse{MemberIDs: []int64{1000, 2000, 4000}}}}
	kc.client = backend
	return kc, backend, matrix
}
func TestGroupCreateFrameworkNameFailureCannotReportSuccessOrRepeatCREATE(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	matrix.failName = true
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	if _, err := kc.CreateGroup(context.Background(), params); err == nil {
		t.Fatal("failed Matrix name handling reported successful binding")
	}
	attempt := kc.login.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)]
	if attempt.Bound || attempt.ChatID != 5000 {
		t.Fatalf("failed binding journal: %+v", attempt)
	}
	matrix.mu.Lock()
	matrix.failName = false
	matrix.mu.Unlock()
	response, err := kc.CreateGroup(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if response.Portal.MXID != params.RoomID || backend.creates != 1 {
		t.Fatal("retry did not preserve intended room and once-only CREATE")
	}
	matrix.mu.Lock()
	defer matrix.mu.Unlock()
	if matrix.name != "Synthetic created group" {
		t.Fatalf("name not converged: %q", matrix.name)
	}
}

func TestGroupCreateFrameworkSourceOutcomesNeverRepeatMutation(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		rejected bool
	}{
		{"transport ambiguity", errors.New("synthetic lost reply"), false},
		{"source rejection", client.StatusError{Command: "CREATE", Status: -310}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			kc, backend, _ := newGroupCreationFramework(t)
			backend.createErr = test.err
			params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
			backend.createHook = func() {
				saved, err := kc.login.Bridge.DB.UserLogin.GetByID(context.Background(), kc.login.ID)
				if err != nil {
					t.Fatal(err)
				}
				attempt, exists := saved.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)]
				if !exists || attempt.ChatID != 0 || attempt.Bound || len(attempt.Participants) != 2 {
					t.Fatal("CREATE preceded durable attempt reservation")
				}
			}
			for range 2 {
				if _, err := kc.CreateGroup(context.Background(), params); err == nil {
					t.Fatal("source failure reported success")
				}
			}
			saved, err := kc.login.Bridge.DB.UserLogin.GetByID(context.Background(), kc.login.ID)
			if err != nil {
				t.Fatal(err)
			}
			attempt := saved.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)]
			if backend.creates != 1 || attempt.Rejected != test.rejected || attempt.ChatID != 0 || attempt.Bound {
				t.Fatal("source outcome did not persist once-only failure")
			}
		})
	}
}

func TestGroupCreateFrameworkMembershipChangeDuringCREATEStopsBinding(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	backend.createHook = func() {
		matrix.mu.Lock()
		defer matrix.mu.Unlock()
		matrix.members["@unselected:test"] = &event.MemberEventContent{Membership: event.MembershipInvite}
	}
	if _, err := kc.CreateGroup(context.Background(), params); err == nil {
		t.Fatal("unselected Matrix member admitted during source creation")
	}
	attempt := kc.login.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)]
	if attempt.ChatID != 5000 || attempt.Bound {
		t.Fatal("confirmed source group lost after permission failure")
	}
	matrix.mu.Lock()
	delete(matrix.members, "@unselected:test")
	matrix.mu.Unlock()
	backend.createHook = nil
	if _, err := kc.CreateGroup(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if backend.creates != 1 {
		t.Fatal("permission repair repeated source creation")
	}
}

func TestGroupCreateFrameworkPortalSaveFailureResumesCachedBinding(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	db := kc.login.Bridge.DB.RawDB
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_created_portal BEFORE UPDATE ON portal WHEN NEW.id='5000' BEGIN SELECT RAISE(ABORT,'synthetic binding failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.CreateGroup(ctx, params); err == nil {
		t.Fatal("failed binding persistence reported success")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_created_portal`); err != nil {
		t.Fatal(err)
	}
	response, err := kc.CreateGroup(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, response.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.MXID != params.RoomID || backend.creates != 1 {
		t.Fatal("cached binding resumed without durable room or repeated CREATE")
	}
}

func TestGroupCreateFrameworkReconcileUnknownUsesSelectedSourceWithoutCREATE(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	backend.createErr = errors.New("synthetic lost source reply")
	if _, err := kc.CreateGroup(ctx, params); !errors.Is(err, errGroupCreateUnresolved) {
		t.Fatalf("lost reply: %v", err)
	}
	backend.memberList.MemberIDs = []int64{1000, 2000, 6000}
	if _, err := kc.ReconcileGroup(ctx, params.RoomID, 5000); err == nil {
		t.Fatal("wrong source roster reconciled")
	}
	if kc.login.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)].ChatID != 0 {
		t.Fatal("wrong source room was recorded")
	}
	backend.memberList.MemberIDs = []int64{1000, 2000, 4000}
	response, err := kc.ReconcileGroup(ctx, params.RoomID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if backend.creates != 1 || response.Portal.MXID != params.RoomID {
		t.Fatal("reconciliation mutated source or bound another room")
	}
	if !kc.login.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)].Bound {
		t.Fatal("reconciliation did not persist completion")
	}
	if _, err = kc.ReconcileGroup(ctx, params.RoomID, 6000); err == nil {
		t.Fatal("completed creation was rebound")
	}
}

func TestGroupCreateFrameworkReconcileStoppedProfileReleasesTemporaryOwner(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	_, fingerprint, err := kc.groupCreateRequest(params)
	if err != nil {
		t.Fatal(err)
	}
	if err = kc.saveGroupAttempt(ctx, params.RoomID, groupCreateAttempt{Fingerprint: fingerprint, Participants: []int64{2000, 4000}}); err != nil {
		t.Fatal(err)
	}
	kc.client = nil
	opens := 0
	kc.open = func() (kakaoClient, error) { opens++; return backend, nil }
	if _, err = kc.ReconcileGroup(ctx, params.RoomID, 5000); err != nil {
		t.Fatal(err)
	}
	if opens != 1 || backend.creates != 0 || backend.closeCalls != 1 {
		t.Fatal("stopped-profile reconciliation failed to release its source owner or mutated")
	}
}

func TestGroupCreateFrameworkMembershipAdmissionWaitsForBinding(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	backend.createHook = func() { close(entered); <-release }
	matrix.failName = true
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	created := make(chan error, 1)
	go func() { _, err := kc.CreateGroup(context.Background(), params); created <- err }()
	<-entered
	queued := 0
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResultSuccess
	}
	kc.sendState = func(status.BridgeState) {}
	handled := make(chan bool, 1)
	started := make(chan struct{})
	notice := events.MemberAdded{ChatID: 5000, LogID: 100}
	go func() { close(started); handled <- kc.handleEvent(backend, notice) }()
	<-started
	select {
	case <-handled:
		t.Fatal("membership admission passed an in-flight CREATE")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-created; err == nil {
		t.Fatal("failed Matrix metadata returned creation success")
	}
	if <-handled || queued != 0 {
		t.Fatal("membership event created a competing portal during failed binding")
	}
	matrix.mu.Lock()
	matrix.failName = false
	matrix.mu.Unlock()
	backend.createHook = nil
	if _, err := kc.CreateGroup(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if !kc.handleEvent(backend, notice) || queued != 1 || backend.creates != 1 {
		t.Fatal("membership replay failed after the single source group was bound")
	}
}

func TestObservedPartialGroupCreationRetainsSourceWithoutBindingOrRetry(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/bridge/group-create-partial-observed.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		CreatorID     int64             `json:"creator_id"`
		Participants  []int64           `json:"participants"`
		ChatData      chatmeta.ChatData `json:"chat_data"`
		MemberIDs     []int64           `json:"member_ids"`
		ExpectedCalls int               `json:"expected_source_create_calls"`
		ExpectedBound bool              `json:"expected_bound"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	kc, backend, _ := newGroupCreationFramework(t)
	kc.userID = fixture.CreatorID
	backend.chatInfo.ChatData = fixture.ChatData
	backend.memberList.MemberIDs = fixture.MemberIDs
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test"}
	for _, participant := range fixture.Participants {
		params.Participants = append(params.Participants, makeUserID(participant))
	}
	for range 2 {
		if _, err = kc.CreateGroup(context.Background(), params); err == nil {
			t.Fatal("partial source roster reported successful binding")
		}
	}
	attempt := kc.login.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)]
	if backend.creates != fixture.ExpectedCalls || attempt.ChatID != fixture.ChatData.ChatID || attempt.Bound != fixture.ExpectedBound {
		t.Fatal("partial creation lost identity, bound incomplete roster, or repeated CREATE")
	}
	portal, err := kc.login.Bridge.DB.Portal.GetByKey(context.Background(), makePortalKey(fixture.ChatData.ChatID, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	if portal != nil && portal.MXID != "" {
		t.Fatal("partial group was bound to Matrix")
	}
}
