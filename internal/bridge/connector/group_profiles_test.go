package connector

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"os"
	"testing"
)

func TestPeriodicGroupProfileRefreshUpdatesExistingGhostWithoutMessage(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Previous"}, {UserID: 4000, Nickname: "Retained"}}
	response, err := kc.CreateGroup(ctx, &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	ghost, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(2000))
	if err != nil {
		t.Fatal(err)
	}
	identity := ghost.Intent.GetMXID()
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Current"}}
	backend.membersErr = errors.New("synthetic unavailable second profile")
	kc.refreshOneGroupProfiles(backend)
	ghost, err = kc.login.Bridge.GetGhostByID(ctx, makeUserID(2000))
	if err != nil {
		t.Fatal(err)
	}
	missing, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(4000))
	if err != nil {
		t.Fatal(err)
	}
	if ghost.Name != "Current" || ghost.Intent.GetMXID() != identity || missing.Name != "Retained" {
		t.Fatalf("periodic refresh names %q/%q, same identity %t, cursor %q, metadata %+v", ghost.Name, missing.Name, ghost.Intent.GetMXID() == identity, kc.profileRefreshAfter, response.Portal.Metadata)
	}
	if backend.creates != 1 || response.Portal.MXID != "!selected:test" {
		t.Fatal("profile refresh created or rebound a group")
	}
}

func TestPeriodicGroupProfileRefreshSkipsRemovedPendingAndStopping(t *testing.T) {
	for _, condition := range []string{"removed", "pending", "stopping"} {
		t.Run(condition, func(t *testing.T) {
			kc, backend, _ := newGroupCreationFramework(t)
			ctx := context.Background()
			response, err := kc.CreateGroup(ctx, &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
			if err != nil {
				t.Fatal(err)
			}
			meta := response.Portal.Metadata.(*KakaoPortalMetadata)
			meta.SourceRemoved = condition == "removed"
			meta.MembershipPending = condition == "pending"
			if err = response.Portal.Save(ctx); err != nil {
				t.Fatal(err)
			}
			kc.stopping = condition == "stopping"
			backend.metadataCalls = nil
			kc.refreshOneGroupProfiles(backend)
			if len(backend.metadataCalls) != 0 || kc.profileRefreshAfter != "" {
				t.Fatal("profile poll requested source metadata outside the active access window")
			}
		})
	}
}

func TestGroupProfilePartialFailureCannotDiscardAuthoritativeRoster(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Current owned synthetic profile"}}
	backend.membersErr = errors.New("synthetic later profile batch failure")
	info, err := kc.GetChatInfo(ctx, p)
	if err != nil {
		t.Fatalf("profile failure discarded a successful authoritative roster: %v", err)
	}
	if info.Members == nil || !info.Members.IsFull || len(info.Members.MemberMap) != 3 {
		t.Fatal("partial profiles changed authoritative membership")
	}
	known := info.Members.MemberMap[makeUserID(2000)]
	if known.UserInfo == nil || known.UserInfo.Name == nil || *known.UserInfo.Name != "Current owned synthetic profile" {
		t.Fatal("successful member profile lost after later failure")
	}
	unknown := info.Members.MemberMap[makeUserID(4000)]
	if unknown.UserInfo != nil {
		t.Fatal("unavailable member profile was fabricated")
	}
}

func TestKnownMemberEmptyProfileClearsPreviousFields(t *testing.T) {
	info := userInfoForMember(chatmeta.Member{UserID: 2000})
	if info.Name == nil || *info.Name != "" {
		t.Fatal("confirmed empty nickname preserves a stale ghost name")
	}
	if info.Avatar == nil || !info.Avatar.Remove {
		t.Fatal("confirmed empty image fields preserve a stale ghost avatar")
	}
}
func TestKnownMemberFullSizeImageFallback(t *testing.T) {
	info := userInfoForMember(chatmeta.Member{UserID: 2000, FullProfileImageURL: "https://example.invalid/full.png"})
	expected := avatarFromURL("https://example.invalid/full.png")
	if info.Avatar == nil || info.Avatar.ID != expected.ID || info.Avatar.Remove {
		t.Fatal("full-size profile image lost when thumbnail missing")
	}
}

func TestExecutedMemberProfileFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-profiles/member-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Input struct {
				Label string
				JSON  map[string]json.RawMessage
			}
			Member struct {
				UserID   int64
				NickName *string
				Image    *string
				Full     *string
			}
			MoooExpected struct {
				Name         string
				ImageURL     string
				AvatarRemove bool
			} `json:"mooo_expected"`
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	empty := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	for _, c := range fixture.Cases {
		t.Run(c.Input.Label, func(t *testing.T) {
			var doc bson.D
			for key, value := range c.Input.JSON {
				var v any
				if key == "userId" {
					var uid int64
					if err = json.Unmarshal(value, &uid); err != nil {
						t.Fatal(err)
					}
					v = uid
				} else {
					if err = json.Unmarshal(value, &v); err != nil {
						t.Fatal(err)
					}
				}
				doc = append(doc, bson.E{Key: key, Value: v})
			}
			body, err := bson.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			member, err := chatmeta.DecodeMember(bson.Raw(body))
			if err != nil {
				t.Fatal(err)
			}
			if member.UserID != c.Member.UserID || member.Nickname != empty(c.Member.NickName) || member.ProfileImageURL != empty(c.Member.Image) || member.FullProfileImageURL != empty(c.Member.Full) {
				t.Fatal("production member decoder differs from executed native fields")
			}
			info := userInfoForMember(member)
			if info.Name == nil || *info.Name != c.MoooExpected.Name || info.Avatar == nil || info.Avatar.Remove != c.MoooExpected.AvatarRemove {
				t.Fatal("production profile projection differs from documented mapping")
			}
			if !info.Avatar.Remove && info.Avatar.ID != avatarFromURL(c.MoooExpected.ImageURL).ID {
				t.Fatal("production avatar mapping differs from documented fallback")
			}
		})
	}
}

func TestGroupProfileRefreshClearsKnownGhostAndPreservesUnavailableGhost(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	ghosts := map[int64]*bridgev2.Ghost{}
	for _, uid := range []int64{2000, 4000} {
		ghost, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(uid))
		if err != nil {
			t.Fatal(err)
		}
		name := "Previous profile"
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{Name: &name, Avatar: &bridgev2.Avatar{ID: "previous", MXC: "mxc://test/previous"}})
		ghosts[uid] = ghost
	}
	backend.members = []chatmeta.Member{{UserID: 2000}}
	backend.membersErr = errors.New("synthetic unavailable later profile")
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise actual framework consumer.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	if !kc.handleEvent(backend, events.ChatMetaChanged{ChatID: 5000, Type: 3}) {
		t.Fatal("profile failure blocked source roster refresh")
	}
	current, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(2000))
	if err != nil {
		t.Fatal(err)
	}
	missing, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(4000))
	if err != nil {
		t.Fatal(err)
	}
	if current.Intent.GetMXID() != ghosts[2000].Intent.GetMXID() || current.Name != "" || current.AvatarMXC != "" || !current.NameSet || !current.AvatarSet {
		t.Fatal("known profile clear retained stale fields or changed ghost identity")
	}
	if missing.Intent.GetMXID() != ghosts[4000].Intent.GetMXID() || missing.Name != "Previous profile" || missing.AvatarMXC != "mxc://test/previous" {
		t.Fatal("unavailable profile erased existing ghost fields or identity")
	}
}
