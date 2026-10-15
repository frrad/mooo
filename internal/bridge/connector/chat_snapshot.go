package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// chatInfoOptions narrows which source chats a chat-info read accepts.
type chatInfoOptions struct {
	// regularGroupOnly rejects anything but a MultiChat with a complete roster.
	regularGroupOnly bool
	// requirePersonalMeta fails a group read whose personal room metadata is
	// unavailable, so a refresh cannot be mistaken for a confirmed clear.
	requirePersonalMeta bool
	inventory           *chatmeta.ChatData
	// storedRoster replaces MEMLIST with a roster from another source.
	// Reconnect discovery passes a managed group's verified roster, whose later
	// changes arrive as membership feeds through pushes and catch-up; group
	// creation passes the CREATE reply's active member list.
	storedRoster []int64
	// metasComplete marks inventory chat metas as the room's full set (a
	// CREATE reply's full-data room), so no room-info read follows.
	metasComplete bool
}

// chatSnapshot is everything one chat-info read fetched from the source:
// CHATINFO (with personal metadata and the display checkpoint applied),
// exactly one MEMLIST, the MEMBER profiles and, for groups, the Boards metas.
type chatSnapshot struct {
	data chatmeta.ChatData
	// roster is the MEMLIST member list as returned; it may be empty.
	roster []int64
	// profiles are the MEMBER results that matched a requested identity.
	profiles []chatmeta.Member
	// moimMetas holds the announcement metadata of a MultiChat.
	moimMetas        []chatmeta.MoimMeta
	moimKnown        bool
	deferred         bool
	metasComplete    bool
	personalRequired bool
}

// profileIDs is the set of identities the projection lists as members: the
// MEMLIST roster, or the display members when MEMLIST returned nothing.
func (snap chatSnapshot) profileIDs() []int64 {
	if len(snap.roster) > 0 {
		return snap.roster
	}
	return snap.data.DisplayUserIDs
}

// chatInfoFromClient reads a source snapshot and projects it. The snapshot is
// returned so callers can check the roster without a second MEMLIST.
func (kc *KakaoClient) chatInfoFromClient(ctx context.Context, portal *bridgev2.Portal, c chatMetaAPI, opts chatInfoOptions) (*bridgev2.ChatInfo, chatSnapshot, error) {
	snap, err := kc.fetchChatSnapshot(ctx, portal, c, opts)
	if err != nil {
		return nil, chatSnapshot{}, err
	}
	info, err := kc.projectChatInfo(portal, snap)
	if err != nil {
		return nil, chatSnapshot{}, err
	}
	if opts.requirePersonalMeta && snap.data.Meta == nil {
		info.Name, info.Avatar = nil, nil
	}
	return info, snap, nil
}

// fetchChatSnapshot performs all source I/O for a chat-info read.
func (kc *KakaoClient) fetchChatSnapshot(ctx context.Context, portal *bridgev2.Portal, c chatMetaAPI, opts chatInfoOptions) (chatSnapshot, error) {
	chatID, err := parseChatID(portal.ID)
	if err != nil {
		return chatSnapshot{}, err
	}
	var data chatmeta.ChatData
	if opts.inventory != nil {
		data = *opts.inventory
	} else {
		response, err := c.ChatInfo(ctx, chatID)
		if err != nil {
			return chatSnapshot{}, err
		}
		data = response.ChatData
	}
	if data.ChatID != chatID {
		return chatSnapshot{}, fmt.Errorf("%w: requested %d, got %d", errChatInfoMismatch, chatID, data.ChatID)
	}
	if opts.regularGroupOnly && data.Type != "MultiChat" {
		return chatSnapshot{}, errors.New("connector: discovery requires a regular group")
	}
	if data.LinkID > 0 {
		return chatSnapshot{}, errUnsupportedOpenChatMetadata
	}
	cache, err := kc.readRoomMetadata(ctx, chatID)
	if err != nil {
		kc.log().Warn().Str("error_class", sourceErrorClass(err)).Msg("Room enrichment is unavailable; preserving verified identities")
		cache = roomMetadataCache{Version: 1, AccountID: kc.userID, ChatID: chatID}
	}
	if data.Type == "MultiChat" {
		if data.Meta == nil {
			data.Meta = cache.Personal
		}
		data, err = kc.checkpointGroupDisplay(ctx, portal, data)
		if err != nil {
			return chatSnapshot{}, err
		}
	}

	// MEMLIST is a UI-originated API in the official client and its stored
	// room token is not exposed by ChatData. An initial bridge sync therefore
	// starts at token zero; later membership updates remain separate work.
	var roster chatmeta.MemberListResponse
	if len(opts.storedRoster) > 0 {
		roster.MemberIDs = opts.storedRoster
	} else if roster, err = c.MemberList(ctx, chatID, 0); err != nil {
		return chatSnapshot{}, err
	}
	if opts.regularGroupOnly && len(roster.MemberIDs) == 0 {
		return chatSnapshot{}, errInvalidMemberRoster
	}
	for _, userID := range roster.MemberIDs {
		if userID <= 0 {
			return chatSnapshot{}, fmt.Errorf("%w: %d", errInvalidMemberRoster, userID)
		}
	}
	snap := chatSnapshot{data: data, roster: append([]int64(nil), roster.MemberIDs...), moimMetas: cache.Boards, moimKnown: !cache.BoardsAt.IsZero(), deferred: true, personalRequired: opts.requirePersonalMeta, metasComplete: opts.metasComplete}
	userIDs := append([]int64(nil), snap.profileIDs()...)
	profiles, err := kc.cachedMemberProfiles(ctx, userIDs)
	if err != nil {
		// The successfully refreshed source roster remains authoritative even
		// when an optional profile batch fails. Keep successful earlier results
		// and leave unresolved identities without fabricated profile fields.
		kc.log().Warn().Int("resolved_profiles", len(profiles)).Int("requested_profiles", len(userIDs)).Str("error_class", sourceErrorClass(err)).
			Msg("Some member profiles are unavailable; preserving the source roster")
	}
	snap.profiles = kc.cacheProfiles(profiles, userIDs)

	kc.enqueueEnrichment(snap, cache)
	if opts.requirePersonalMeta {
		kc.invalidateEnrichment(ctx, chatID, refreshPersonal)
	}

	return snap, nil
}

// cacheProfiles remembers MEMBER profiles for GetUserInfo and returns those
// that answer a requested identity other than the local account.
func (kc *KakaoClient) cacheProfiles(profiles []chatmeta.Member, requested []int64) []chatmeta.Member {
	wanted := make(map[int64]struct{}, len(requested))
	for _, userID := range requested {
		wanted[userID] = struct{}{}
	}
	accepted := make([]chatmeta.Member, 0, len(profiles))
	kc.cacheMu.Lock()
	defer kc.cacheMu.Unlock()
	for _, profile := range profiles {
		if _, ok := wanted[profile.UserID]; !ok || profile.UserID <= 0 || profile.UserID == kc.userID {
			continue
		}
		kc.profiles[profile.UserID] = profile
		accepted = append(accepted, profile)
	}
	return accepted
}

// projectMembers maps a snapshot's roster and profiles to a member list. It is
// full only when the snapshot carries a complete roster.
func (kc *KakaoClient) projectMembers(snap chatSnapshot) *bridgev2.ChatMemberList {
	members := bridgev2.ChatMemberMap{}.Set(bridgev2.ChatMember{
		EventSender: kc.selfSender(),
		Membership:  event.MembershipJoin,
	})
	for _, profile := range snap.profiles {
		members.Set(bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{Sender: makeUserID(profile.UserID)},
			Membership:  event.MembershipJoin,
			UserInfo:    kc.snapshotUserInfo(profile, snap.deferred),
		})
	}
	// If MEMBER returned no profile for an ID, retain the source-supported
	// membership identity without fabricating profile fields.
	for _, userID := range snap.profileIDs() {
		if userID <= 0 || userID == kc.userID {
			continue
		}
		if _, exists := members[makeUserID(userID)]; exists {
			continue
		}
		members.Set(bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{Sender: makeUserID(userID)},
			Membership:  event.MembershipJoin,
		})
	}
	list := &bridgev2.ChatMemberList{IsFull: len(snap.roster) > 0, MemberMap: members}
	if list.IsFull {
		list.TotalMemberCount = len(members)
	}
	return list
}

// projectChatInfo maps a source snapshot to bridge chat info without I/O.
func (kc *KakaoClient) projectChatInfo(portal *bridgev2.Portal, snap chatSnapshot) (*bridgev2.ChatInfo, error) {
	data := snap.data
	completeRoster := len(snap.roster) > 0
	info := &bridgev2.ChatInfo{CanBackfill: kc.historyEnabled(), Members: kc.projectMembers(snap)}
	members := info.Members.MemberMap
	// Notifications on/off is the account's personal setting for this chat.
	// It maps only to the Matrix user's own mute, never to shared state.
	if data.PushAlertSet {
		mutedUntil := bridgev2.Unmuted
		if !data.PushAlert {
			mutedUntil = event.MutedForever
		}
		info.UserLocal = &bridgev2.UserLocalPortalInfo{MutedUntil: &mutedUntil}
	}
	if !completeRoster && data.ActiveMemberCount > 0 {
		info.Members.TotalMemberCount = int(data.ActiveMemberCount)
	}
	if data.Type == "MultiChat" {
		display, err := chatmeta.ProjectGroupDisplay(data)
		if err != nil {
			return nil, err
		}
		name := display.Name
		if name == "" {
			name = chatName(chatmeta.ChatData{DisplayNicknames: data.DisplayNicknames})
		}
		if name != "" {
			info.Name = &name
		}
		if display.AvatarKnown {
			avatarURL := display.ImageURL
			if avatarURL == "" {
				avatarURL = display.FullImageURL
			}
			if avatarURL == "" {
				info.Avatar = &bridgev2.Avatar{Remove: true}
			} else {
				info.Avatar = avatarFromURL(avatarURL, kc)
			}
		}
	} else {
		if name := chatName(data); name != "" {
			info.Name = &name
		}
		if data.Meta != nil {
			info.Avatar = avatarFromURL(data.Meta.ImageURL, kc)
		}
	}
	if data.Type == "DirectChat" && completeRoster {
		otherUserID, count := networkid.UserID(""), 0
		for userID := range members {
			if userID != makeUserID(kc.userID) {
				otherUserID, count = userID, count+1
			}
		}
		if count == 1 {
			roomType := database.RoomTypeDM
			info.Type = &roomType
			info.Members.OtherUserID = otherUserID
		}
	}
	if data.Type == "MultiChat" && (snap.moimKnown || !snap.deferred) {
		announcement, err := announcementTopicInfo(portal, snap.moimMetas)
		if errors.Is(err, chatmeta.ErrUnsupportedAnnouncement) {
			kc.log().Warn().Msg("Unsupported Boards announcement content; keeping current Matrix topic")
		} else if err != nil {
			return nil, err
		} else {
			info.Topic, info.ExtraUpdates = announcement.Topic, announcement.ExtraUpdates
		}
	}
	if snap.deferred && info.Avatar != nil && info.Avatar.Get != nil {
		if info.Avatar.ID == portal.AvatarID && portal.AvatarSet && portal.AvatarMXC != "" {
			info.Avatar.MXC, info.Avatar.Hash, info.Avatar.Get = portal.AvatarMXC, portal.AvatarHash, nil
		} else {
			info.Avatar = nil
		}
	}
	return info, nil
}

var (
	errRosterIncomplete = errors.New("source roster is incomplete or differs in size")
	errRosterUnexpected = errors.New("source roster has an unexpected participant")
)

// rosterMatches checks the projected member set (the MEMLIST roster plus the
// local account, which the projection always lists) against expected. With
// allowSubset, a roster missing expected identities is accepted.
func (kc *KakaoClient) rosterMatches(snap chatSnapshot, expected map[networkid.UserID]bool, allowSubset bool) error {
	if len(snap.roster) == 0 {
		return errRosterIncomplete
	}
	actual := map[networkid.UserID]bool{makeUserID(kc.userID): true}
	for _, userID := range snap.roster {
		actual[makeUserID(userID)] = true
	}
	if !allowSubset && len(actual) != len(expected) {
		return errRosterIncomplete
	}
	for userID := range actual {
		if !expected[userID] {
			return errRosterUnexpected
		}
	}
	return nil
}
