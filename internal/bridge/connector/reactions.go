package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

const reactionLookupTimeout = 30 * time.Second
const reactionNoticeSuppression = 24 * time.Hour
const reactionNoticeLimit = 256

var (
	errReactionUnsupported   = errors.New("connector: Kakao reaction is unsupported")
	errReactionTarget        = errors.New("connector: invalid Kakao reaction target")
	errReactionSender        = errors.New("connector: reaction sender is not this login")
	errReactionRevision      = errors.New("connector: invalid Kakao reaction revision")
	errReactionMutation      = errors.New("connector: Kakao reaction mutation failed")
	errReactionLookup        = errors.New("connector: Kakao reaction lookup failed")
	errReactionTargetMissing = errors.New("connector: reaction target message missing")
)

var _ bridgev2.ReactionHandlingNetworkAPI = (*KakaoClient)(nil)

// legacyReaction is one of KakaoTalk's six legacy reaction selections.
type legacyReaction struct {
	typeID  reactions.Type
	emoji   string
	emojiID networkid.EmojiID
}

// legacyReactions is deliberately explicit and indexed by reaction type; the
// Cancel slot is empty. Aggregate item IDs are not selection values and are
// never used to derive this mapping.
var legacyReactions = [...]legacyReaction{
	reactions.Heart:    {reactions.Heart, "❤️", "kakao:legacy:1"},
	reactions.Like:     {reactions.Like, "👍", "kakao:legacy:2"},
	reactions.Check:    {reactions.Check, "✅", "kakao:legacy:3"},
	reactions.Laugh:    {reactions.Laugh, "😆", "kakao:legacy:4"},
	reactions.Surprise: {reactions.Surprise, "😮", "kakao:legacy:5"},
	reactions.Sad:      {reactions.Sad, "😢", "kakao:legacy:6"},
}

// legacyReactionsByEmoji maps each legacy emoji, with and without a trailing
// variation selector, to its reaction.
var legacyReactionsByEmoji = func() map[string]legacyReaction {
	byEmoji := make(map[string]legacyReaction, 2*len(legacyReactions))
	for _, entry := range legacyReactions[reactions.Heart:] {
		byEmoji[entry.emoji] = entry
		byEmoji[normalizeReactionEmoji(entry.emoji)] = entry
	}
	return byEmoji
}()

func normalizeReactionEmoji(value string) string {
	return strings.TrimSuffix(value, "\ufe0f")
}

func reactionForEmoji(value string) (legacyReaction, bool) {
	entry, ok := legacyReactionsByEmoji[value]
	if ok {
		return entry, true
	}
	entry, ok = legacyReactionsByEmoji[normalizeReactionEmoji(value)]
	return entry, ok
}

func (kc *KakaoClient) reactionClient() (reactionAPI, error) {
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	return c, nil
}

func validateMatrixReaction(kc *KakaoClient, msg *bridgev2.MatrixReaction) (reactions.Request, legacyReaction, error) {
	if kc == nil || kc.login == nil || msg == nil || msg.TargetMessage == nil || msg.Portal == nil || msg.Event == nil || msg.Content == nil {
		return reactions.Request{}, legacyReaction{}, errReactionTarget
	}
	if msg.Event.Sender != kc.login.UserMXID {
		return reactions.Request{}, legacyReaction{}, errReactionSender
	}
	chatID, logID, err := parseMessageID(msg.TargetMessage.ID)
	if err != nil || chatID <= 0 || logID <= 0 {
		return reactions.Request{}, legacyReaction{}, errReactionTarget
	}
	portalChat, err := parseChatID(msg.Portal.ID)
	if err != nil || portalChat != chatID || msg.Portal.Receiver != kc.login.ID {
		return reactions.Request{}, legacyReaction{}, errReactionTarget
	}
	var metadata *KakaoMessageMetadata
	switch value := msg.TargetMessage.Metadata.(type) {
	case *KakaoMessageMetadata:
		metadata = value
	case KakaoMessageMetadata:
		copy := value
		metadata = &copy
	}
	if metadata != nil && (metadata.ChatID != chatID || metadata.LogID != logID) {
		return reactions.Request{}, legacyReaction{}, errReactionTarget
	}
	if (msg.TargetMessage.Room.ID != "" && msg.TargetMessage.Room.ID != msg.Portal.ID) ||
		(msg.TargetMessage.Room.Receiver != "" && msg.TargetMessage.Room.Receiver != kc.login.ID) {
		return reactions.Request{}, legacyReaction{}, errReactionTarget
	}
	entry, ok := reactionForEmoji(msg.Content.RelatesTo.Key)
	if !ok {
		return reactions.Request{}, legacyReaction{}, errReactionUnsupported
	}
	linkID := int64(0)
	if metadata != nil {
		linkID = metadata.LinkID
	}
	return reactions.Request{ChatID: chatID, LogID: logID, LinkID: linkID, Type: entry.typeID}, entry, nil
}

func (kc *KakaoClient) PreHandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	_, entry, err := validateMatrixReaction(kc, msg)
	if err != nil {
		return bridgev2.MatrixReactionPreResponse{}, err
	}
	return bridgev2.MatrixReactionPreResponse{
		SenderID: makeUserID(kc.userID),
		EmojiID:  entry.emojiID,
		Emoji:    entry.emoji,
		// No framework-wide limit: Kakao keeps one legacy selection per user,
		// but the same user's mini reactions are independent. HandleMatrixReaction
		// replaces only the previous legacy selection.
	}, nil
}

func (kc *KakaoClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	// See HandleMatrixMessage: no connector gate under the portal event lock.
	request, _, err := validateMatrixReaction(kc, msg)
	if err != nil {
		return nil, err
	}
	if err = kc.checkSourceAccess(ctx, request.ChatID, msg.Portal); err != nil {
		return nil, err
	}
	api, err := kc.reactionClient()
	if err != nil {
		return nil, err
	}
	if _, err = api.React(ctx, request); err != nil {
		return nil, classifyReactionFailure(errReactionMutation, err)
	}
	kc.removeReplacedLegacyReactions(ctx, msg)
	return nil, nil
}

// removeReplacedLegacyReactions redacts and forgets this account's other
// legacy reactions on the target after KakaoTalk accepted a new legacy
// selection, which replaces them. Mini reactions are left untouched.
func (kc *KakaoClient) removeReplacedLegacyReactions(ctx context.Context, msg *bridgev2.MatrixReaction) {
	if msg.Portal == nil || msg.Portal.Bridge == nil || msg.TargetMessage == nil || msg.PreHandleResp == nil {
		return
	}
	bridge := msg.Portal.Bridge
	existing, err := bridge.DB.Reaction.GetAllToMessageBySender(ctx, msg.Portal.Receiver, msg.TargetMessage.ID, msg.PreHandleResp.SenderID)
	if err != nil {
		kc.log().Warn().Err(err).Msg("Could not load replaced Kakao legacy reactions")
		return
	}
	for _, old := range existing {
		if old.EmojiID == msg.PreHandleResp.EmojiID || !strings.HasPrefix(string(old.EmojiID), "kakao:legacy:") {
			continue
		}
		if _, err := bridge.Bot.SendMessage(ctx, msg.Portal.MXID, event.EventRedaction, &event.Content{
			Parsed: &event.RedactionEventContent{Redacts: old.MXID},
		}, nil); err != nil {
			kc.log().Warn().Err(err).Msg("Could not redact replaced Kakao legacy reaction")
		}
		if err := bridge.DB.Reaction.Delete(ctx, old); err != nil {
			kc.log().Warn().Err(err).Msg("Could not delete replaced Kakao legacy reaction")
		}
	}
}

func (kc *KakaoClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	if kc == nil || kc.login == nil || msg == nil || msg.TargetReaction == nil || msg.Portal == nil || msg.Event == nil {
		return errReactionTarget
	}
	if ctx == nil {
		return errReactionLookup
	}
	// See HandleMatrixMessage: no connector gate under the portal event lock.
	if msg.Event.Sender != kc.login.UserMXID {
		return errReactionSender
	}
	if msg.TargetReaction.SenderID != "" && msg.TargetReaction.SenderID != makeUserID(kc.userID) {
		return errReactionSender
	}
	chatID, logID, err := parseMessageID(msg.TargetReaction.MessageID)
	if err != nil || chatID <= 0 || logID <= 0 {
		return errReactionTarget
	}
	portalChat, err := parseChatID(msg.Portal.ID)
	if err != nil || portalChat != chatID || msg.Portal.Receiver != kc.login.ID {
		return errReactionTarget
	}
	if err = kc.checkSourceAccess(ctx, chatID, msg.Portal); err != nil {
		return err
	}
	if (msg.TargetReaction.Room.ID != "" && msg.TargetReaction.Room.ID != msg.Portal.ID) ||
		(msg.TargetReaction.Room.Receiver != "" && msg.TargetReaction.Room.Receiver != kc.login.ID) {
		return errReactionTarget
	}
	wanted, ok := legacyReactionEmojiID(msg.TargetReaction.EmojiID)
	if !ok {
		entry, ok := reactionForEmoji(msg.TargetReaction.Emoji)
		if !ok {
			return errReactionUnsupported
		}
		wanted = entry.typeID
	}
	api, err := kc.reactionClient()
	if err != nil {
		return err
	}
	// Confirm the row being redacted is still the authenticated user's remote
	// reaction. This prevents a stale Matrix redaction from canceling a newer
	// replacement sent by the same user.
	lookupCtx, cancel := context.WithTimeout(ctx, reactionLookupTimeout)
	defer cancel()
	members, err := api.ReactionMembers(lookupCtx, chatID, logID)
	if err != nil {
		return classifyReactionLookup(err)
	}
	if _, err := reactionSyncUsers(members, kc.userID, kc.login.ID); err != nil {
		return fmt.Errorf("%w: %w", errReactionLookup, reactions.ErrLookupFailed)
	}
	if !containsReactionUser(members.Members[wanted], kc.userID) {
		return nil
	}
	if _, err = api.React(lookupCtx, reactions.Request{ChatID: chatID, LogID: logID, Type: reactions.Cancel}); err != nil {
		return classifyReactionFailure(errReactionMutation, err)
	}
	return nil
}

// classifyReactionFailure returns only stable, redacted categories. Backend
// errors can contain URLs, response bodies, or authorization material, so the
// original error is deliberately not wrapped into the connector result.
func classifyReactionFailure(base, cause error) error {
	category := reactions.ErrOutcomeUnknown
	reason := error(nil)
	if errors.Is(cause, reactions.ErrRejected) {
		category = reactions.ErrOutcomeUnconfirmed
		reason = reactions.ErrRejected
	} else if errors.Is(cause, reactions.ErrInvalidResponse) {
		reason = reactions.ErrInvalidResponse
	} else if errors.Is(cause, reactions.ErrTransport) {
		reason = reactions.ErrTransport
	}
	if errors.Is(cause, context.Canceled) {
		reason = context.Canceled
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = context.DeadlineExceeded
	}
	if errors.Is(cause, reactions.ErrInvalidRequest) {
		return fmt.Errorf("%w: %w", base, reactions.ErrInvalidRequest)
	}
	if reason != nil {
		return fmt.Errorf("%w: %w: %w", base, category, reason)
	}
	return fmt.Errorf("%w: %w", base, category)
}

func classifyReactionLookup(cause error) error {
	if errors.Is(cause, context.Canceled) {
		return fmt.Errorf("%w: %w: %w", errReactionLookup, reactions.ErrLookupFailed, context.Canceled)
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w: %w", errReactionLookup, reactions.ErrLookupFailed, context.DeadlineExceeded)
	}
	if errors.Is(cause, reactions.ErrTransport) {
		return fmt.Errorf("%w: %w: %w", errReactionLookup, reactions.ErrLookupFailed, reactions.ErrTransport)
	}
	if errors.Is(cause, reactions.ErrInvalidResponse) {
		return fmt.Errorf("%w: %w: %w", errReactionLookup, reactions.ErrLookupFailed, reactions.ErrInvalidResponse)
	}
	if errors.Is(cause, reactions.ErrRejected) {
		return fmt.Errorf("%w: %w: %w", errReactionLookup, reactions.ErrLookupFailed, reactions.ErrRejected)
	}
	return fmt.Errorf("%w: %w", errReactionLookup, reactions.ErrLookupFailed)
}

func containsReactionUser(values []int64, userID int64) bool {
	for _, value := range values {
		if value == userID {
			return true
		}
	}
	return false
}

func (kc *KakaoClient) reactionRemote(parent context.Context, c kakaoClient, change events.ReactionChanged) (bridgev2.RemoteEvent, error) {
	if change.ChatID <= 0 || change.LogID <= 0 || change.LinkID < 0 || change.Revision <= 0 {
		return nil, errReactionRevision
	}
	for _, item := range change.Items {
		if item.Kind != 1 && (change.MetadataType != 2 || item.Kind != 2) {
			return nil, errReactionUnsupported
		}
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, reactionLookupTimeout)
	defer cancel()
	stored, err := kc.storedReactionRevision(ctx, change)
	if err != nil {
		return nil, err
	}
	if change.Revision <= stored {
		return nil, nil
	}
	members, err := c.ReactionMembers(ctx, change.ChatID, change.LogID)
	if err != nil {
		return nil, err
	}
	if change.MetadataType != 2 && (members.Revision < change.Revision || members.Revision < stored) {
		return nil, errReactionRevision
	}
	if change.MetadataType == 2 {
		legacyChange := change
		legacyChange.MetadataType = 1
		legacyStored, err := kc.storedReactionRevision(ctx, legacyChange)
		if err != nil {
			return nil, err
		}
		if members.Revision < legacyStored {
			return nil, errReactionRevision
		}
	}
	users, err := reactionSyncUsers(members, kc.userID, kc.login.ID)
	// A message with only mini reactions has no legacy revision yet. The
	// observed members response is exactly {"revision":0}; it is an empty
	// legacy roster, not an invalid mini revision or an attribution failure.
	if change.MetadataType == 2 && members.Revision == 0 && len(members.Members) == 0 && len(members.Fields) == 1 {
		if raw, ok := members.Fields["revision"]; ok && string(raw) == "0" {
			users = make(map[networkid.UserID]*bridgev2.ReactionSyncUser)
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	appliedRevision := members.Revision
	if change.MetadataType == 2 {
		mini, err := c.MiniReactionDetails(ctx, change.ChatID, change.LinkID, change.LogID)
		if err != nil {
			return nil, err
		}
		if err := addMiniReactionUsers(users, mini, change, kc.userID, kc.login.ID); err != nil {
			return nil, err
		}
		appliedRevision = change.Revision
	}
	return &kakaoReactionSync{ReactionSync: simplevent.ReactionSync{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReactionSync,
			PortalKey: makePortalKey(change.ChatID, kc.login.ID),
			// Reaction updates never create a portal or target placeholder. The
			// framework ignores the event when the bridged message is absent.
			CreatePortal: false,
			StreamOrder:  change.Revision,
		},
		TargetMessage: makeMessageID(change.ChatID, change.LogID),
		Reactions:     &bridgev2.ReactionSyncData{Users: users, HasAllUsers: true},
	}, AppliedRevision: appliedRevision, IncludesMini: change.MetadataType == 2}, nil
}

// reactionDeliveryEvents expands an authoritative aggregate into individual
// framework operations. ReactionSync currently logs Matrix redaction failures
// and still returns success, which would advance the Kakao checkpoint while
// leaving stale reactions behind. Individual operations propagate send errors;
// callers additionally verify the database postcondition after each one.
// applyReactionChange delivers one reaction change to Matrix and persists its
// applied revision. A lookup error is returned for the caller's policy: a
// live push reports it, a resync keeps its cursor.
func (kc *KakaoClient) applyReactionChange(c kakaoClient, reaction events.ReactionChanged) (bool, error) {
	remote, err := kc.reactionRemote(context.Background(), c, reaction)
	if err != nil {
		return false, err
	}
	if remote == nil {
		return true, nil
	}
	remotes := []bridgev2.RemoteEvent{remote}
	if syncEvent, ok := remote.(*kakaoReactionSync); ok && kc.login != nil && kc.login.Bridge != nil && kc.login.Bridge.DB != nil {
		remotes, err = kc.reactionDeliveryEvents(context.Background(), syncEvent)
		if err != nil {
			if errors.Is(err, errReactionTargetMissing) {
				kc.log().Debug().Msg("Kakao reaction target is not bridged; leaving revision for replay")
				return false, nil
			}
			kc.log().Warn().Err(err).Msg("Kakao reaction delivery plan could not be built")
			return false, nil
		}
	}
	allIgnored := true
	for _, delivery := range remotes {
		result := kc.queue(delivery)
		if !committable(result) {
			kc.log().Warn().Err(result.Error).Msg("Kakao reaction update was not confirmed as bridged")
			return false, nil
		}
		if !result.Ignored {
			allIgnored = false
		}
		if syncEvent, ok := delivery.(*simplevent.Reaction); ok && syncEvent.Type == bridgev2.RemoteEventReactionRemove && kc.login != nil && kc.login.Bridge != nil && kc.login.Bridge.DB != nil {
			row, queryErr := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(context.Background(), kc.login.ID, syncEvent.TargetMessage, syncEvent.Sender.Sender, syncEvent.EmojiID)
			if queryErr != nil || row != nil {
				kc.log().Warn().Err(queryErr).Msg("Kakao reaction removal was not confirmed in the database")
				return false, nil
			}
		}
	}
	if len(remotes) == 0 || !allIgnored {
		appliedRevision := reaction.Revision
		if syncEvent, ok := remote.(*kakaoReactionSync); ok {
			appliedRevision = syncEvent.AppliedRevision
		}
		if err := kc.persistReactionRevision(context.Background(), reaction, appliedRevision); err != nil {
			kc.log().Warn().Msg("Kakao reaction revision could not be persisted")
			return false, nil
		}
	}
	return true, nil
}

func (kc *KakaoClient) reactionDeliveryEvents(ctx context.Context, sync *kakaoReactionSync) ([]bridgev2.RemoteEvent, error) {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return nil, errors.New("connector: reaction database is unavailable")
	}
	target := sync.TargetMessage
	message, err := kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, target)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, errReactionTargetMissing
	}
	existing, err := kc.login.Bridge.DB.Reaction.GetAllToMessage(ctx, kc.login.ID, target)
	if err != nil {
		return nil, err
	}
	type reactionKey struct {
		sender networkid.UserID
		emoji  networkid.EmojiID
	}
	existingByKey := make(map[reactionKey]*database.Reaction, len(existing))
	for _, row := range existing {
		existingByKey[reactionKey{row.SenderID, row.EmojiID}] = row
	}
	type addition struct {
		sender bridgev2.EventSender
		emoji  networkid.EmojiID
		text   string
	}
	var additions []addition
	for senderID, user := range sync.Reactions.Users {
		if user == nil {
			continue
		}
		sender := bridgev2.EventSender{Sender: senderID}
		if senderID == makeUserID(kc.userID) {
			sender.IsFromMe = true
			sender.SenderLogin = kc.login.ID
		}
		for _, reaction := range user.Reactions {
			if reaction == nil || reaction.EmojiID == "" {
				continue
			}
			key := reactionKey{senderID, reaction.EmojiID}
			if old := existingByKey[key]; old != nil && old.MXID != "" {
				delete(existingByKey, key)
				continue
			}
			additions = append(additions, addition{sender: sender, emoji: reaction.EmojiID, text: reaction.Emoji})
			delete(existingByKey, key)
		}
	}
	sort.Slice(additions, func(i, j int) bool {
		if additions[i].sender.Sender != additions[j].sender.Sender {
			return additions[i].sender.Sender < additions[j].sender.Sender
		}
		return additions[i].emoji < additions[j].emoji
	})
	sort.Slice(existing, func(i, j int) bool {
		if existing[i].SenderID != existing[j].SenderID {
			return existing[i].SenderID < existing[j].SenderID
		}
		return existing[i].EmojiID < existing[j].EmojiID
	})
	var result []bridgev2.RemoteEvent
	for _, add := range additions {
		result = append(result, &simplevent.Reaction{
			EventMeta:     simplevent.EventMeta{Type: bridgev2.RemoteEventReaction, PortalKey: sync.PortalKey, Sender: add.sender, StreamOrder: sync.StreamOrder},
			TargetMessage: target, EmojiID: add.emoji, Emoji: add.text,
		})
	}
	if sync.Reactions.HasAllUsers {
		for _, old := range existing {
			if _, known := legacyReactionEmojiID(old.EmojiID); !known && (!sync.IncludesMini || !strings.HasPrefix(string(old.EmojiID), "kakao:mini:")) {
				continue
			}
			if _, stillPresent := existingByKey[reactionKey{old.SenderID, old.EmojiID}]; !stillPresent {
				continue
			}
			result = append(result, &simplevent.Reaction{
				EventMeta:     simplevent.EventMeta{Type: bridgev2.RemoteEventReactionRemove, PortalKey: sync.PortalKey, Sender: bridgev2.EventSender{Sender: old.SenderID}, StreamOrder: sync.StreamOrder},
				TargetMessage: target, EmojiID: old.EmojiID, Emoji: old.Emoji,
			})
		}
	}
	return result, nil
}

func legacyReactionEmojiID(value networkid.EmojiID) (reactions.Type, bool) {
	for _, entry := range legacyReactions[reactions.Heart:] {
		if entry.emojiID == value {
			return entry.typeID, true
		}
	}
	return 0, false
}

type kakaoReactionSync struct {
	simplevent.ReactionSync
	AppliedRevision int64
	IncludesMini    bool
}

func (kc *KakaoClient) reportReactionFailure(change events.ReactionChanged, err error) bool {
	kind := "lookup"
	if errors.Is(err, errReactionUnsupported) {
		kind = "unsupported"
	}
	key := fmt.Sprintf("%d:%d:%s", change.ChatID, change.LogID, kind)
	now := time.Now()
	kc.reactionNoticeMu.Lock()
	if kc.reactionNotices == nil {
		kc.reactionNotices = make(map[string]time.Time)
	}
	for existing, timestamp := range kc.reactionNotices {
		if now.Sub(timestamp) >= reactionNoticeSuppression {
			delete(kc.reactionNotices, existing)
		}
	}
	if len(kc.reactionNotices) >= reactionNoticeLimit {
		var oldestKey string
		var oldest time.Time
		for existing, timestamp := range kc.reactionNotices {
			if oldestKey == "" || timestamp.Before(oldest) {
				oldestKey, oldest = existing, timestamp
			}
		}
		delete(kc.reactionNotices, oldestKey)
	}
	last, seen := kc.reactionNotices[key]
	if !seen || now.Sub(last) >= reactionNoticeSuppression {
		seen = false
	}
	kc.reactionNoticeMu.Unlock()
	if seen {
		return true
	}
	body := "A Kakao reaction update could not be synchronized."
	if kind == "unsupported" {
		body = "A Kakao reaction update uses an unsupported reaction type."
	}
	notice := newMessage(
		kc.messageMeta(change.ChatID, change.LogID, 0, 0),
		networkid.MessageID(fmt.Sprintf("reaction-sync-error:%d:%d:%s", change.ChatID, change.LogID, kind)),
		noticeData{Body: body},
		convertNoticeWithMetadata,
	)
	result := committable(kc.queue(notice))
	if result {
		kc.reactionNoticeMu.Lock()
		kc.reactionNotices[key] = now
		kc.reactionNoticeMu.Unlock()
	}
	return result
}

func reactionSyncUsers(members reactions.MembersResponse, selfID int64, loginID networkid.UserLoginID) (map[networkid.UserID]*bridgev2.ReactionSyncUser, error) {
	if members.Revision <= 0 {
		return nil, errReactionRevision
	}
	for key, raw := range members.Fields {
		if numeric, err := strconv.ParseInt(key, 10, 64); err == nil {
			if strconv.FormatInt(numeric, 10) != key || numeric < int64(reactions.Heart) || numeric > int64(reactions.Sad) {
				return nil, errReactionUnsupported
			}
			var values []int64
			if json.Unmarshal(raw, &values) != nil || values == nil {
				return nil, errReactionUnsupported
			}
			for _, value := range values {
				if value <= 0 {
					return nil, errReactionUnsupported
				}
			}
		}
	}
	users := make(map[networkid.UserID]*bridgev2.ReactionSyncUser)
	seenTypes := make(map[networkid.UserID]reactions.Type)
	for rawType := int64(reactions.Heart); rawType <= int64(reactions.Sad); rawType++ {
		typeID := reactions.Type(rawType)
		values := members.Members[typeID]
		for _, userID := range values {
			if userID <= 0 {
				return nil, errReactionUnsupported
			}
			id := makeUserID(userID)
			if prior, exists := seenTypes[id]; exists {
				if prior != typeID {
					return nil, fmt.Errorf("%w: user appears in multiple legacy buckets", errReactionUnsupported)
				}
				continue
			}
			seenTypes[id] = typeID
			entry := legacyReactions[typeID]
			sender := bridgev2.EventSender{Sender: id}
			if userID == selfID {
				sender.IsFromMe = true
				sender.SenderLogin = loginID
			}
			users[id] = &bridgev2.ReactionSyncUser{
				Reactions:       []*bridgev2.BackfillReaction{{Sender: sender, EmojiID: entry.emojiID, Emoji: entry.emoji}},
				HasAllReactions: true,
			}
		}
	}
	return users, nil
}

func (kc *KakaoClient) storedReactionRevision(ctx context.Context, change events.ReactionChanged) (int64, error) {
	key := reactionRevisionCacheKey(change)
	kc.reactionMu.Lock()
	cached := kc.reactionRevisions[key]
	kc.reactionMu.Unlock()
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return cached, nil
	}
	msg, err := kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, makeMessageID(change.ChatID, change.LogID))
	if err != nil || msg == nil {
		return 0, err
	}
	switch metadata := msg.Metadata.(type) {
	case *KakaoMessageMetadata:
		if metadata != nil && reactionMetadataRevision(metadata, change) > cached {
			return reactionMetadataRevision(metadata, change), nil
		}
	case KakaoMessageMetadata:
		if reactionMetadataRevision(metadata, change) > cached {
			return reactionMetadataRevision(metadata, change), nil
		}
	}
	return cached, nil
}

func (kc *KakaoClient) persistReactionRevision(ctx context.Context, change events.ReactionChanged, appliedRevision int64) error {
	if appliedRevision <= 0 {
		return errReactionRevision
	}
	key := reactionRevisionCacheKey(change)
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		kc.reactionMu.Lock()
		if appliedRevision > kc.reactionRevisions[key] {
			kc.reactionRevisions[key] = appliedRevision
		}
		kc.reactionMu.Unlock()
		return nil
	}
	msg, err := kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, makeMessageID(change.ChatID, change.LogID))
	if err != nil || msg == nil {
		return err
	}
	metadata, ok := msg.Metadata.(*KakaoMessageMetadata)
	if !ok || metadata == nil {
		if value, valueOK := msg.Metadata.(KakaoMessageMetadata); valueOK {
			metadata = &value
			msg.Metadata = metadata
		} else {
			return errReactionRevision
		}
	}
	if appliedRevision <= reactionMetadataRevision(metadata, change) {
		return nil
	}
	if change.MetadataType == 2 {
		metadata.MiniReactionRevision = appliedRevision
	} else {
		metadata.ReactionRevision = appliedRevision
	}
	if err := kc.login.Bridge.DB.Message.Update(ctx, msg); err != nil {
		return err
	}
	kc.reactionMu.Lock()
	if appliedRevision > kc.reactionRevisions[key] {
		kc.reactionRevisions[key] = appliedRevision
	}
	kc.reactionMu.Unlock()
	return nil
}

func reactionRevisionCacheKey(change events.ReactionChanged) string {
	key := string(makeMessageID(change.ChatID, change.LogID))
	if change.MetadataType == 2 {
		key += ":mini"
	}
	return key
}
func reactionMetadataRevision(value any, change events.ReactionChanged) int64 {
	var meta KakaoMessageMetadata
	switch v := value.(type) {
	case KakaoMessageMetadata:
		meta = v
	case *KakaoMessageMetadata:
		if v != nil {
			meta = *v
		}
	}
	if change.MetadataType == 2 {
		return meta.MiniReactionRevision
	}
	return meta.ReactionRevision
}
