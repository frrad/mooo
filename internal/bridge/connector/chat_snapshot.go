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
	moimMetas []chatmeta.MoimMeta
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
	return info, snap, nil
}

// fetchChatSnapshot performs all source I/O for a chat-info read.
func (kc *KakaoClient) fetchChatSnapshot(ctx context.Context, portal *bridgev2.Portal, c chatMetaAPI, opts chatInfoOptions) (chatSnapshot, error) {
	chatID, err := parseChatID(portal.ID)
	if err != nil {
		return chatSnapshot{}, err
	}
	response, err := c.ChatInfo(ctx, chatID)
	if err != nil {
		return chatSnapshot{}, err
	}
	data := response.ChatData
	if data.ChatID != chatID {
		return chatSnapshot{}, fmt.Errorf("%w: requested %d, got %d", errChatInfoMismatch, chatID, data.ChatID)
	}
	if opts.regularGroupOnly && data.Type != "MultiChat" {
		return chatSnapshot{}, errors.New("connector: discovery requires a regular group")
	}
	if data.LinkID > 0 {
		return chatSnapshot{}, errUnsupportedOpenChatMetadata
	}
	if data.Type == "MultiChat" {
		if data.Meta == nil {
			data.Meta, err = c.PersonalMeta(ctx, chatID)
			if err != nil {
				return chatSnapshot{}, err
			}
		}
		if opts.requirePersonalMeta && data.Meta == nil {
			return chatSnapshot{}, errors.New("connector: personal room metadata unavailable; refresh did not confirm a clear")
		}
		data, err = kc.checkpointGroupDisplay(ctx, portal, data)
		if err != nil {
			return chatSnapshot{}, err
		}
	}

	// MEMLIST is a UI-originated API in the official client and its stored
	// room token is not exposed by ChatData. An initial bridge sync therefore
	// starts at token zero; later membership updates remain separate work.
	roster, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
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
	snap := chatSnapshot{data: data, roster: append([]int64(nil), roster.MemberIDs...)}
	userIDs := append([]int64(nil), snap.profileIDs()...)
	profiles, err := kc.resolveMemberProfiles(ctx, c, chatID, userIDs)
	if err != nil {
		if data.Type != "MultiChat" {
			return chatSnapshot{}, err
		}
		// The successfully refreshed source roster remains authoritative even
		// when an optional profile batch fails. Keep successful earlier results
		// and leave unresolved identities without fabricated profile fields.
		kc.log().Warn().Int("resolved_profiles", len(profiles)).Int("requested_profiles", len(userIDs)).Str("error_class", sourceErrorClass(err)).
			Msg("Some member profiles are unavailable; preserving the source roster")
	}
	snap.profiles = kc.cacheProfiles(profiles, userIDs)

	if data.Type == "MultiChat" {
		moim, err := c.MoimMeta(ctx, chatID)
		if err != nil {
			return chatSnapshot{}, err
		}
		if moim.ChatID != chatID {
			return chatSnapshot{}, errChatInfoMismatch
		}
		snap.moimMetas = moim.Metas
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

// projectChatInfo maps a source snapshot to bridge chat info without I/O.
func (kc *KakaoClient) projectChatInfo(portal *bridgev2.Portal, snap chatSnapshot) (*bridgev2.ChatInfo, error) {
	data := snap.data
	completeRoster := len(snap.roster) > 0
	members := bridgev2.ChatMemberMap{}.Set(bridgev2.ChatMember{
		EventSender: kc.selfSender(),
		Membership:  event.MembershipJoin,
	})
	for _, profile := range snap.profiles {
		members.Set(bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{Sender: makeUserID(profile.UserID)},
			Membership:  event.MembershipJoin,
			UserInfo:    userInfoForMember(profile, kc),
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

	info := &bridgev2.ChatInfo{CanBackfill: kc.historyEnabled(), Members: &bridgev2.ChatMemberList{
		IsFull:    completeRoster,
		MemberMap: members,
	}}
	// Notifications on/off is the account's personal setting for this chat.
	// It maps only to the Matrix user's own mute, never to shared state.
	if data.PushAlertSet {
		mutedUntil := bridgev2.Unmuted
		if !data.PushAlert {
			mutedUntil = event.MutedForever
		}
		info.UserLocal = &bridgev2.UserLocalPortalInfo{MutedUntil: &mutedUntil}
	}
	if completeRoster {
		info.Members.TotalMemberCount = len(members)
	} else if data.ActiveMemberCount > 0 {
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
	if data.Type == "MultiChat" {
		announcement, err := announcementTopicInfo(portal, snap.moimMetas)
		if errors.Is(err, chatmeta.ErrUnsupportedAnnouncement) {
			kc.log().Warn().Msg("Unsupported Boards announcement content; keeping current Matrix topic")
		} else if err != nil {
			return nil, err
		} else {
			info.Topic, info.ExtraUpdates = announcement.Topic, announcement.ExtraUpdates
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
