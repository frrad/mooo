package connector

import (
	"context"
	"errors"
	"slices"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

var errMembershipPending = errors.New("connector: Matrix membership has not converged with the source roster; forwarding is paused")

const stateGroupMembershipPending status.BridgeStateErrorCode = "kakao-group-membership-pending"
const stateGroupMembershipInvalid status.BridgeStateErrorCode = "kakao-group-membership-invalid"

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{stateGroupMembershipInvalid: "Kakao delivered an undecodable membership notice. Forwarding stopped to prevent using stale access. Repair the decoder, then reconnect to verify the source roster.", stateGroupMembershipPending: "Matrix group membership has not converged with the source roster. Repair room membership or permissions, then reconnect to refresh before forwarding resumes."})
}

func (kc *KakaoClient) saveMembershipCheckpoint(ctx context.Context, p *bridgev2.Portal, pending bool, roster []int64) error {
	var sourceRemoved bool
	return kc.savePortalMeta(ctx, p, func(next *KakaoPortalMetadata) {
		next.GroupMembershipManaged = true
		next.MembershipPending = pending
		next.MembershipRoster = slices.Clone(roster)
		if !pending {
			next.SourceRemoved = false
		}
		sourceRemoved = next.SourceRemoved
	}, func(got *KakaoPortalMetadata) bool {
		return got.GroupMembershipManaged && got.MembershipPending == pending && got.SourceRemoved == sourceRemoved && slices.Equal(got.MembershipRoster, roster)
	})
}

// refreshGroupMembership uses a fresh full source roster rather than a possibly
// delayed notice delta. It clears access only after verifying Matrix convergence.
func (kc *KakaoClient) refreshGroupMembership(ctx context.Context, c kakaoClient, chatID int64, create bool) error {
	return kc.refreshGroupMembershipInventory(ctx, c, chatID, create, nil)
}
func (kc *KakaoClient) refreshGroupMembershipInventory(ctx context.Context, c kakaoClient, chatID int64, create bool, inventory *chatmeta.ChatData) error {
	return kc.refreshGroupMembershipFrom(ctx, c, chatID, create, chatInfoOptions{regularGroupOnly: true, inventory: inventory})
}

func (kc *KakaoClient) refreshGroupMembershipFrom(ctx context.Context, c kakaoClient, chatID int64, create bool, opts chatInfoOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := kc.pauseGroupMembership(ctx, chatID)
	if err != nil {
		return err
	}
	info, snap, err := kc.chatInfoFromClient(ctx, p, c, opts)
	if err != nil {
		return err
	}
	return kc.convergeGroupMembership(ctx, p, info, snap.roster, create, kc.historyEnabled())
}

// pauseGroupMembership blocks forwarding and persists the pause before any
// source or Matrix work, so a failed or interrupted change stays paused.
func (kc *KakaoClient) pauseGroupMembership(ctx context.Context, chatID int64) (*bridgev2.Portal, error) {
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return nil, err
	}
	kc.cacheMu.Lock()
	if kc.sourceBlocked == nil {
		kc.sourceBlocked = map[int64]error{}
	}
	kc.sourceBlocked[chatID] = errMembershipPending
	kc.cacheMu.Unlock()
	if err = kc.saveMembershipCheckpoint(ctx, p, true, nil); err != nil {
		return nil, err
	}
	return p, nil
}

// convergeGroupMembership applies a source roster to Matrix and clears the
// pause only after verifying that Matrix membership matches it.
func (kc *KakaoClient) convergeGroupMembership(ctx context.Context, p *bridgev2.Portal, info *bridgev2.ChatInfo, roster []int64, create, backfill bool) error {
	chatID, err := parseChatID(p.ID)
	if err != nil {
		return err
	}
	expected := map[networkid.UserID]bool{}
	for _, member := range roster {
		user := makeUserID(member)
		if member <= 0 || expected[user] {
			return errInvalidMemberRoster
		}
		expected[user] = true
	}
	if !expected[makeUserID(kc.userID)] {
		if err = kc.recordSourceRemoval(ctx, chatID); err != nil {
			return err
		}
		return errSourceAccessRemoved
	}
	if kc.rosterMatches(chatSnapshot{roster: roster}, expected, false) != nil {
		return errInvalidMemberRoster
	}
	if err = kc.saveMembershipCheckpoint(ctx, p, true, roster); err != nil {
		return err
	}
	key := p.PortalKey
	resync := &simplevent.ChatResync{EventMeta: simplevent.EventMeta{Type: bridgev2.RemoteEventChatResync, PortalKey: key, Sender: kc.selfSender(), CreatePortal: create}, GetChatInfoFunc: func(_ context.Context, target *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
		if target == nil || target.PortalKey != key {
			return nil, errChatInfoMismatch
		}
		return info, nil
	}}
	if backfill {
		resync.CheckNeedsBackfillFunc = func(context.Context, *database.Message) (bool, error) { return true, nil }
	}
	if !committable(kc.queue(resync)) {
		return errMembershipPending
	}
	// Portal creation may have supplied the room ID through the framework cache.
	p, err = kc.login.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return err
	}
	if p.MXID == "" {
		return errMembershipPending
	}
	members, err := kc.login.Bridge.Matrix.GetMembers(ctx, p.MXID)
	if err != nil {
		return err
	}
	for user := range expected {
		ghost, ghostErr := kc.login.Bridge.GetGhostByID(ctx, user)
		if ghostErr != nil {
			return ghostErr
		}
		if !kc.joinedAs(ctx, members, user, ghost.Intent.GetMXID()) {
			return errMembershipPending
		}
	}
	for mxid, member := range members {
		user, isGhost := kc.login.Bridge.Matrix.ParseGhostMXID(mxid)
		if isGhost && !expected[user] && member.Membership.IsInviteOrJoin() {
			return errMembershipPending
		}
	}
	if err = kc.saveMembershipCheckpoint(ctx, p, false, roster); err != nil {
		return err
	}
	kc.cacheMu.Lock()
	delete(kc.sourceBlocked, chatID)
	kc.cacheMu.Unlock()
	return nil
}

// membershipFeed is the member delta carried by a NEWMEM or DELMEM notice, or
// by the equivalent catch-up feed.
type membershipFeed struct {
	invitees []int64
	leaver   int64
}

// feedRoster applies a feed to the group's stored verified roster, as the
// official client's NEWMEM and DELMEM consumers apply it to the stored room.
// It reports false when the feed cannot be applied locally: the group has no
// verified roster, or the feed names the account itself or an invalid ID.
// The official client sends CHATINFO for an unknown room or a self invite;
// the bridge reads the full roster in those cases.
func (kc *KakaoClient) feedRoster(ctx context.Context, chatID int64, feed membershipFeed) ([]int64, bool) {
	stored := kc.verifiedRoster(ctx, chatID)
	if len(stored) == 0 {
		return nil, false
	}
	if feed.leaver != 0 {
		if feed.leaver < 0 || feed.leaver == kc.userID || len(feed.invitees) != 0 {
			return nil, false
		}
		return slices.DeleteFunc(stored, func(userID int64) bool { return userID == feed.leaver }), true
	}
	if len(feed.invitees) == 0 {
		return nil, false
	}
	for _, userID := range feed.invitees {
		if userID <= 0 || userID == kc.userID {
			return nil, false
		}
		if !slices.Contains(stored, userID) {
			stored = append(stored, userID)
		}
	}
	return stored, true
}

// applyMembershipFeed converges Matrix with a roster derived from a feed. It
// sends no request itself; invitees get MEMBER through the enrichment queue,
// as the official client sends MEMBER for NEWMEM invitees.
func (kc *KakaoClient) applyMembershipFeed(ctx context.Context, chatID int64, roster []int64, invitees []int64) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := kc.pauseGroupMembership(ctx, chatID)
	if err != nil {
		return err
	}
	snap := chatSnapshot{data: chatmeta.ChatData{ChatID: chatID, Type: "MultiChat"}, roster: roster, deferred: true}
	if snap.profiles, err = kc.cachedMemberProfiles(ctx, roster); err != nil {
		kc.log().Warn().Str("error_class", sourceErrorClass(err)).Msg("Stored member profiles are unavailable; preserving the source roster")
	}
	info := &bridgev2.ChatInfo{CanBackfill: kc.historyEnabled(), Members: kc.projectMembers(snap)}
	if err = kc.convergeGroupMembership(ctx, p, info, roster, false, false); err != nil {
		return err
	}
	kc.rememberFeedRoster(chatID, roster, invitees)
	return nil
}

func (kc *KakaoClient) managedMembershipEvent(ctx context.Context, c kakaoClient, chatID int64, create bool) (bool, bool) {
	return kc.managedMembershipChange(ctx, c, chatID, create, nil)
}

func (kc *KakaoClient) managedMembershipChange(ctx context.Context, c kakaoClient, chatID int64, create bool, feed *membershipFeed) (bool, bool) {
	p, err := kc.store.portalByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return true, false
	}
	if p != nil && p.RoomType == database.RoomTypeDM {
		return false, false
	}
	var roster []int64
	applicable := false
	if feed != nil {
		roster, applicable = kc.feedRoster(ctx, chatID, *feed)
	}
	if applicable {
		err = kc.applyMembershipFeed(ctx, chatID, roster, feed.invitees)
	} else {
		err = kc.refreshGroupMembership(ctx, c, chatID, create)
	}
	if err != nil {
		kc.log().Warn().Err(err).Int64("kakao_chat_id", chatID).Msg("Group membership refresh failed")
		if !errors.Is(err, errSourceAccessRemoved) {
			kc.scheduleMembershipRetry(chatID, 1)
		}
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupMembershipPending})
	} else if kc.IsLoggedIn() {
		kc.sendReadyState()
	}
	return true, err == nil
}

// handleMembershipEvent applies a member change to managed group membership.
// It reports whether the event was consumed and, if so, whether it succeeded.
func (kc *KakaoClient) handleMembershipEvent(ctx context.Context, c kakaoClient, evt events.Event) (handled, success bool) {
	switch notice := evt.(type) {
	case events.MemberAdded:
		feed := membershipFeed{invitees: []int64{}}
		for _, member := range notice.Members {
			feed.invitees = append(feed.invitees, member.UserID)
		}
		return kc.managedMembershipChange(ctx, c, notice.ChatID, true, &feed)
	case events.MemberRemoved:
		// A group without a room converges by creating one, as login
		// discovery would; a refresh that cannot create it stays paused.
		return kc.managedMembershipChange(ctx, c, notice.ChatID, true, &membershipFeed{leaver: notice.UserID})
	}
	return false, false
}

// joinedAs reports whether the expected member is joined. bridgev2 represents
// the logged-in user by their double puppet when one is available, and by
// their ghost otherwise.
func (kc *KakaoClient) joinedAs(ctx context.Context, members map[id.UserID]*event.MemberEventContent, user networkid.UserID, ghostMXID id.UserID) bool {
	if m := members[ghostMXID]; m != nil && m.Membership == event.MembershipJoin {
		return true
	}
	if !kc.IsThisUser(ctx, user) || kc.login.User.DoublePuppet(ctx) == nil {
		return false
	}
	m := members[kc.login.UserMXID]
	return m != nil && m.Membership == event.MembershipJoin
}
