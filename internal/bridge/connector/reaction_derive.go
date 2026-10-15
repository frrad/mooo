package connector

import (
	"context"
	"strconv"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// reactionConfirmedLimit bounds the per-session confirmation map.
const reactionConfirmedLimit = 4096

// reactionConfirmation records that a message's stored legacy roster was
// complete at revision on one session, because a members read or a derived
// change produced it there. Pushes on one session arrive in order, so a push
// that follows a confirmed revision describes every change since.
type reactionConfirmation struct {
	client   kakaoClient
	revision int64
}

func (kc *KakaoClient) confirmLegacyReactions(c kakaoClient, change events.ReactionChanged, revision int64) {
	if change.MetadataType != 1 {
		return
	}
	key := reactionLookupKey{change.ChatID, change.LogID, 1}
	kc.reactionLookupMu.Lock()
	defer kc.reactionLookupMu.Unlock()
	if kc.reactionConfirmed == nil || len(kc.reactionConfirmed) >= reactionConfirmedLimit {
		kc.reactionConfirmed = make(map[reactionLookupKey]reactionConfirmation)
	}
	kc.reactionConfirmed[key] = reactionConfirmation{client: c, revision: revision}
}

func (kc *KakaoClient) legacyReactionsConfirmed(c kakaoClient, change events.ReactionChanged, stored int64) bool {
	key := reactionLookupKey{change.ChatID, change.LogID, 1}
	kc.reactionLookupMu.Lock()
	defer kc.reactionLookupMu.Unlock()
	confirmed, ok := kc.reactionConfirmed[key]
	return ok && confirmed.client == c && confirmed.revision == stored && stored > 0
}

// deriveLegacyReactions builds a legacy push's complete roster from the
// stored one without a members read, when the push accounts for it:
//
//   - extra.my is this account's own pick, as the official client reads it;
//   - extra.userId and extra.type, which the official client ignores, name one
//     actor's new selection;
//   - otherwise a count that dropped by one on a selection only one other
//     member held (optionally reappearing on another selection) names that
//     member.
//
// The derived roster must reproduce the push's counts exactly; anything else
// falls back to the members read. It returns nil when no derivation applies.
func (kc *KakaoClient) deriveLegacyReactions(ctx context.Context, c kakaoClient, change events.ReactionChanged, stored int64) (*kakaoReactionSync, error) {
	if change.MetadataType != 1 || !change.Extra.HasOwnPick || !kc.legacyReactionsConfirmed(c, change, stored) {
		return nil, nil
	}
	rows, err := kc.store.reactionsToMessage(ctx, kc.login.ID, makeMessageID(change.ChatID, change.LogID))
	if err != nil {
		return nil, err
	}
	picks := make(map[int64]reactions.Type)
	for _, row := range rows {
		typeID, ok := legacyReactionEmojiID(row.EmojiID)
		if !ok {
			continue
		}
		userID, err := strconv.ParseInt(string(row.SenderID), 10, 64)
		if err != nil || userID <= 0 {
			return nil, nil
		}
		if prior, exists := picks[userID]; exists && prior != typeID {
			return nil, nil
		}
		picks[userID] = typeID
	}
	setPick := func(userID int64, pick int64) {
		if pick == int64(reactions.Cancel) {
			delete(picks, userID)
		} else {
			picks[userID] = reactions.Type(pick)
		}
	}
	setPick(kc.userID, change.Extra.OwnPick)
	if change.Extra.HasActor {
		if change.Extra.ActorID == kc.userID {
			if change.Extra.ActorPick != change.Extra.OwnPick {
				return nil, nil
			}
		} else {
			setPick(change.Extra.ActorID, change.Extra.ActorPick)
		}
	}
	want := make(map[reactions.Type]int64)
	for _, item := range change.Items {
		typeID, err := strconv.ParseInt(item.ID, 10, 64)
		if item.Kind != 1 || err != nil || typeID < int64(reactions.Heart) || typeID > int64(reactions.Sad) {
			return nil, nil
		}
		if item.Count > 0 {
			want[reactions.Type(typeID)] = item.Count
		}
	}
	if !change.Extra.HasActor && !legacyCountsMatch(picks, want) {
		resolveUniqueHolder(picks, want, kc.userID)
	}
	if !legacyCountsMatch(picks, want) {
		return nil, nil
	}
	users := make(map[networkid.UserID]*bridgev2.ReactionSyncUser, len(picks))
	for userID, typeID := range picks {
		entry := legacyReactions[typeID]
		sender := bridgev2.EventSender{Sender: makeUserID(userID)}
		if userID == kc.userID {
			sender.IsFromMe = true
			sender.SenderLogin = kc.login.ID
		}
		users[sender.Sender] = &bridgev2.ReactionSyncUser{
			Reactions:       []*bridgev2.BackfillReaction{{Sender: sender, EmojiID: entry.emojiID, Emoji: entry.emoji}},
			HasAllReactions: true,
		}
	}
	return &kakaoReactionSync{ReactionSync: simplevent.ReactionSync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventReactionSync,
			PortalKey:    makePortalKey(change.ChatID, kc.login.ID),
			CreatePortal: false,
			StreamOrder:  change.Revision,
		},
		TargetMessage: makeMessageID(change.ChatID, change.LogID),
		Reactions:     &bridgev2.ReactionSyncData{Users: users, HasAllUsers: true},
	}, AppliedRevision: change.Revision}, nil
}

func legacyCountsMatch(picks map[int64]reactions.Type, want map[reactions.Type]int64) bool {
	have := make(map[reactions.Type]int64)
	for _, typeID := range picks {
		have[typeID]++
	}
	if len(have) != len(want) {
		return false
	}
	for typeID, count := range want {
		if have[typeID] != count {
			return false
		}
	}
	return true
}

// resolveUniqueHolder applies the one change a count difference can
// attribute without an actor: one selection lost exactly one reaction, at
// most one other selection gained exactly one, and exactly one member other
// than this account held the lost selection.
func resolveUniqueHolder(picks map[int64]reactions.Type, want map[reactions.Type]int64, self int64) {
	have := make(map[reactions.Type]int64)
	for _, typeID := range picks {
		have[typeID]++
	}
	var lost, gained []reactions.Type
	for typeID := reactions.Heart; typeID <= reactions.Sad; typeID++ {
		switch want[typeID] - have[typeID] {
		case 0:
		case -1:
			lost = append(lost, typeID)
		case 1:
			gained = append(gained, typeID)
		default:
			return
		}
	}
	if len(lost) != 1 || len(gained) > 1 {
		return
	}
	holder := int64(0)
	for userID, typeID := range picks {
		if typeID != lost[0] || userID == self {
			continue
		}
		if holder != 0 {
			return
		}
		holder = userID
	}
	if holder == 0 {
		return
	}
	if len(gained) == 1 {
		picks[holder] = gained[0]
	} else {
		delete(picks, holder)
	}
}
