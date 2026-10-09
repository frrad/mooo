package connector

import (
	"context"
	"errors"
	"github.com/frrad/mooo/internal/protocol/chat"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2/database"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestGroupCreationValidationBeforeMutation(t *testing.T) {
	kc := &KakaoClient{userID: 1000}
	api, ok := any(kc).(bridgev2.GroupCreatingNetworkAPI)
	if !ok {
		t.Fatal("explicit group creation interface missing")
	}
	for _, params := range []*bridgev2.GroupCreateParams{
		nil,
		{Type: "regular", Participants: []networkid.UserID{"2000", "4000"}},
		{Type: "secret", RoomID: id.RoomID("!synthetic:example.org"), Participants: []networkid.UserID{"2000", "4000"}},
		{Type: "regular", RoomID: id.RoomID("!synthetic:example.org"), Participants: []networkid.UserID{"2000", "2000"}},
		{Type: "regular", RoomID: id.RoomID("!synthetic:example.org"), Participants: []networkid.UserID{"2000", "invalid"}},
		{Type: "regular", RoomID: id.RoomID("!synthetic:example.org"), Participants: []networkid.UserID{"2000", "1000"}},
	} {
		if _, err := api.CreateGroup(context.Background(), params); err == nil || errors.Is(err, bridgev2.ErrNotLoggedIn) {
			t.Fatal("invalid group creation accepted")
		}
	}
}

func TestGroupCreationJournalSurvivesReloadAndSuppressesCREATE(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "journal.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	br, err := newFrameworkConversionBridge(ctx, raw, &frameworkPersistenceIntent{})
	if err != nil {
		t.Fatal(err)
	}
	user, err := br.GetUserByMXID(ctx, id.UserID("@owner:test"))
	if err != nil {
		t.Fatal(err)
	}
	record := &database.UserLogin{BridgeID: br.ID, UserMXID: user.MXID, ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{Profile: "synthetic-profile"}}
	if err = br.DB.UserLogin.Insert(ctx, record); err != nil {
		t.Fatal(err)
	}
	backend := &countingGroupCreateClient{fakeKakao: &fakeKakao{chatInfoErr: errors.New("synthetic snapshot failure")}}
	login := &bridgev2.UserLogin{UserLogin: record, Bridge: br, User: user}
	kc := newKakaoClient(login, testSelfID, nil)
	kc.client = backend
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	_, fingerprint, err := kc.groupCreateRequest(params)
	if err != nil {
		t.Fatal(err)
	}
	attempt := groupCreateAttempt{Fingerprint: fingerprint}
	if err = kc.saveGroupAttempt(ctx, params.RoomID, attempt); err != nil {
		t.Fatal(err)
	}
	reload, err := br.DB.UserLogin.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	kc.login.UserLogin = reload
	if _, err = kc.CreateGroup(ctx, params); !errors.Is(err, errGroupCreateUnresolved) {
		t.Fatalf("unknown attempt: %v", err)
	}
	if err = kc.resumeGroupCreates(ctx, backend); !errors.Is(err, errGroupCreateUnresolved) {
		t.Fatalf("startup unknown attempt: %v", err)
	}
	attempt.ChatID = 5000
	if err = kc.saveGroupAttempt(ctx, params.RoomID, attempt); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = kc.CreateGroup(ctx, params); err == nil {
			t.Fatal("failed snapshot reported success")
		}
	}
	if backend.creates != 0 {
		t.Fatalf("journal replay made %d CREATE calls", backend.creates)
	}
	// A failed journal update must retain the last durable state in memory and DB.
	if _, err = raw.RawDB.ExecContext(ctx, `CREATE TRIGGER fail_journal BEFORE UPDATE ON user_login BEGIN SELECT RAISE(ABORT,'synthetic save failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := attempt
	failed.Bound = true
	if err = kc.saveGroupAttempt(ctx, params.RoomID, failed); err == nil {
		t.Fatal("save failure ignored")
	}
	meta := kc.login.Metadata.(*UserLoginMetadata)
	if meta.GroupCreates[string(params.RoomID)].Bound {
		t.Fatal("failed save advanced in-memory journal")
	}
	reload, err = br.DB.UserLogin.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reload.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)].Bound {
		t.Fatal("failed save advanced durable journal")
	}
	if _, err = raw.RawDB.ExecContext(ctx, `DROP TRIGGER fail_journal`); err != nil {
		t.Fatal(err)
	}
	if err = br.DB.UserLogin.Delete(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if err = kc.saveGroupAttempt(ctx, params.RoomID, failed); err == nil {
		t.Fatal("zero-row update authorized mutation")
	}
	if meta.GroupCreates[string(params.RoomID)].Bound {
		t.Fatal("missing login advanced in-memory journal")
	}
}

type countingGroupCreateClient struct {
	*fakeKakao
	creates       int
	createErr     error
	createHook    func()
	invites       int
	inviteErr     error
	inviteWarning string
	inviteHook    func(chat.AddMembersRequest)
}

func (c *countingGroupCreateClient) AddMembers(_ context.Context, r chat.AddMembersRequest) (chat.AddMembersResponse, error) {
	c.invites++
	if c.inviteHook != nil {
		c.inviteHook(r)
	}
	return chat.AddMembersResponse{Warning: c.inviteWarning}, c.inviteErr
}

func (c *countingGroupCreateClient) CreateChat(context.Context, chat.CreateRequest) (chat.CreateResponse, error) {
	c.creates++
	if c.createHook != nil {
		c.createHook()
	}
	return chat.CreateResponse{ChatID: 5000}, c.createErr
}
