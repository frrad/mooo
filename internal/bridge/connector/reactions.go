package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

const reactionLookupTimeout = 30 * time.Second
const reactionNoticeSuppression = 24 * time.Hour

var (
	errReactionUnsupported = errors.New("connector: Kakao reaction is unsupported")
	errReactionTarget      = errors.New("connector: invalid Kakao reaction target")
	errReactionSender      = errors.New("connector: reaction sender is not this login")
	errReactionRevision    = errors.New("connector: invalid Kakao reaction revision")
)

type reactionAPI interface {
	React(context.Context, reactions.Request) (reactions.Response, error)
	ReactionMembers(context.Context, int64, int64) (reactions.MembersResponse, error)
}

var _ bridgev2.ReactionHandlingNetworkAPI = (*KakaoClient)(nil)

// legacyReactionTable is deliberately explicit. Aggregate item IDs are not
// selection values and are never used to derive this mapping.
var legacyReactionTable = map[string]struct {
	typeID  reactions.Type
	emoji   string
	emojiID networkid.EmojiID
}{
	"❤":  {reactions.Heart, "❤️", "kakao:legacy:1"},
	"❤️": {reactions.Heart, "❤️", "kakao:legacy:1"},
	"👍":  {reactions.Like, "👍", "kakao:legacy:2"},
	"✅":  {reactions.Check, "✅", "kakao:legacy:3"},
	"😆":  {reactions.Laugh, "😆", "kakao:legacy:4"},
	"😮":  {reactions.Surprise, "😮", "kakao:legacy:5"},
	"😢":  {reactions.Sad, "😢", "kakao:legacy:6"},
}

func normalizeReactionEmoji(value string) string {
	return strings.TrimSuffix(value, "\ufe0f")
}

func reactionForEmoji(value string) (struct {
	typeID  reactions.Type
	emoji   string
	emojiID networkid.EmojiID
}, bool) {
	entry, ok := legacyReactionTable[value]
	if ok {
		return entry, true
	}
	entry, ok = legacyReactionTable[normalizeReactionEmoji(value)]
	return entry, ok
}

func (kc *KakaoClient) reactionClient() (reactionAPI, error) {
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	reaction, ok := c.(reactionAPI)
	if !ok {
		return nil, errors.New("connector: Kakao client does not support reactions")
	}
	return reaction, nil
}

func validateMatrixReaction(kc *KakaoClient, msg *bridgev2.MatrixReaction) (reactions.Request, struct {
	typeID  reactions.Type
	emoji   string
	emojiID networkid.EmojiID
}, error) {
	if kc == nil || kc.login == nil || msg == nil || msg.TargetMessage == nil || msg.Portal == nil || msg.Event == nil {
		return reactions.Request{}, struct {
			typeID  reactions.Type
			emoji   string
			emojiID networkid.EmojiID
		}{}, errReactionTarget
	}
	if msg.Event.Sender != kc.login.UserMXID {
		return reactions.Request{}, struct {
			typeID  reactions.Type
			emoji   string
			emojiID networkid.EmojiID
		}{}, errReactionSender
	}
	chatID, logID, err := parseMessageID(msg.TargetMessage.ID)
	if err != nil || chatID <= 0 || logID <= 0 {
		return reactions.Request{}, struct {
			typeID  reactions.Type
			emoji   string
			emojiID networkid.EmojiID
		}{}, errReactionTarget
	}
	portalChat, err := parseChatID(msg.Portal.ID)
	if err != nil || portalChat != chatID || msg.Portal.Receiver != kc.login.ID {
		return reactions.Request{}, struct {
			typeID  reactions.Type
			emoji   string
			emojiID networkid.EmojiID
		}{}, errReactionTarget
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
		return reactions.Request{}, struct {
			typeID  reactions.Type
			emoji   string
			emojiID networkid.EmojiID
		}{}, errReactionTarget
	}
	entry, ok := reactionForEmoji(msg.Content.RelatesTo.Key)
	if !ok {
		return reactions.Request{}, struct {
			typeID  reactions.Type
			emoji   string
			emojiID networkid.EmojiID
		}{}, errReactionUnsupported
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
		SenderID:     makeUserID(kc.userID),
		EmojiID:      entry.emojiID,
		Emoji:        entry.emoji,
		MaxReactions: 1,
	}, nil
}

func (kc *KakaoClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	request, _, err := validateMatrixReaction(kc, msg)
	if err != nil {
		return nil, err
	}
	api, err := kc.reactionClient()
	if err != nil {
		return nil, err
	}
	if _, err = api.React(ctx, request); err != nil {
		return nil, err
	}
	return nil, nil
}

func (kc *KakaoClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	if kc == nil || kc.login == nil || msg == nil || msg.TargetReaction == nil || msg.Portal == nil || msg.Event == nil {
		return errReactionTarget
	}
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
	var wanted reactions.Type
	switch msg.TargetReaction.EmojiID {
	case "kakao:legacy:1":
		wanted = reactions.Heart
	case "kakao:legacy:2":
		wanted = reactions.Like
	case "kakao:legacy:3":
		wanted = reactions.Check
	case "kakao:legacy:4":
		wanted = reactions.Laugh
	case "kakao:legacy:5":
		wanted = reactions.Surprise
	case "kakao:legacy:6":
		wanted = reactions.Sad
	default:
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
	members, err := api.ReactionMembers(ctx, chatID, logID)
	if err != nil {
		return err
	}
	if !containsReactionUser(members.Members[wanted], kc.userID) {
		return nil
	}
	_, err = api.React(ctx, reactions.Request{ChatID: chatID, LogID: logID, Type: reactions.Cancel})
	return err
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
		if item.Kind != 1 {
			return nil, errReactionUnsupported
		}
	}
	api, ok := c.(reactionAPI)
	if !ok {
		return nil, errors.New("connector: Kakao client does not support reaction attribution")
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
	members, err := api.ReactionMembers(ctx, change.ChatID, change.LogID)
	if err != nil {
		return nil, err
	}
	users, err := reactionSyncUsers(members)
	if err != nil {
		return nil, err
	}
	return &simplevent.ReactionSync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventReactionSync,
			PortalKey:    makePortalKey(change.ChatID, kc.login.ID),
			CreatePortal: true,
			StreamOrder:  change.Revision,
		},
		TargetMessage: makeMessageID(change.ChatID, change.LogID),
		Reactions:     &bridgev2.ReactionSyncData{Users: users, HasAllUsers: true},
	}, nil
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

func reactionSyncUsers(members reactions.MembersResponse) (map[networkid.UserID]*bridgev2.ReactionSyncUser, error) {
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
			entry := legacyReactionByType(typeID)
			users[id] = &bridgev2.ReactionSyncUser{
				Reactions:       []*bridgev2.BackfillReaction{{Sender: bridgev2.EventSender{Sender: id}, EmojiID: entry.emojiID, Emoji: entry.emoji}},
				HasAllReactions: true,
			}
		}
	}
	return users, nil
}

func legacyReactionByType(typeID reactions.Type) struct {
	typeID  reactions.Type
	emoji   string
	emojiID networkid.EmojiID
} {
	for _, entry := range legacyReactionTable {
		if entry.typeID == typeID {
			return entry
		}
	}
	return struct {
		typeID  reactions.Type
		emoji   string
		emojiID networkid.EmojiID
	}{}
}

func (kc *KakaoClient) storedReactionRevision(ctx context.Context, change events.ReactionChanged) (int64, error) {
	key := string(makeMessageID(change.ChatID, change.LogID))
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
		if metadata != nil && metadata.ReactionRevision > cached {
			return metadata.ReactionRevision, nil
		}
	case KakaoMessageMetadata:
		if metadata.ReactionRevision > cached {
			return metadata.ReactionRevision, nil
		}
	}
	return cached, nil
}

func (kc *KakaoClient) persistReactionRevision(ctx context.Context, change events.ReactionChanged) error {
	key := string(makeMessageID(change.ChatID, change.LogID))
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		kc.reactionMu.Lock()
		if change.Revision > kc.reactionRevisions[key] {
			kc.reactionRevisions[key] = change.Revision
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
	if change.Revision <= metadata.ReactionRevision {
		return nil
	}
	metadata.ReactionRevision = change.Revision
	if err := kc.login.Bridge.DB.Message.Update(ctx, msg); err != nil {
		return err
	}
	kc.reactionMu.Lock()
	if change.Revision > kc.reactionRevisions[key] {
		kc.reactionRevisions[key] = change.Revision
	}
	kc.reactionMu.Unlock()
	return nil
}
