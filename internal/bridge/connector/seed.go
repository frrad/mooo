package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

// seedLastMessages shows each never-delivered direct and regular group chat
// with the last message the login inventory already carries. The official
// Mac client likewise stores that message at LOGINLIST and marks the older
// interval as missing instead of fetching it. Seeding requests no history: a
// direct room is created from inventory display data, and groups already have
// rooms from discovery. Committing the seeded message lets later reconnects
// catch up from it. Like catch-up, an undelivered seed ends the connection,
// because its queued commit must precede the chat's live commits.
func (kc *KakaoClient) seedLastMessages(ctx context.Context, c kakaoClient) error {
	chats, err := c.ListChats(ctx)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if !seedable(chat) {
			continue
		}
		evt, err := c.SeedLastMessage(chat, kc.isSeedMessage)
		if err != nil {
			kc.log().Warn().Err(err).Int64("kakao_chat_id", chat.ChatID).Msg("Kakao inventory last message could not be decoded; not seeding chat")
			continue
		}
		if evt == nil {
			continue
		}
		release, err := kc.holdInventoryRoom(ctx, chat)
		if err != nil {
			return err
		}
		delivered := kc.handleEvent(c, evt)
		release()
		if !delivered {
			return fmt.Errorf("seed chat: %w", errDeliveryNotConfirmed)
		}
		if chat.NewMessageCount == 0 {
			// The account has read the chat, so the seeded message is not
			// shown as unread. The receipt is best effort, as live ones are.
			_, logID, _ := events.MessagePosition(evt)
			kc.handleReadState(events.ReadStateChanged{ChatID: chat.ChatID, UserID: kc.userID, Watermark: logID})
		}
	}
	return nil
}

// seedable selects the chats the bridge shows from inventory alone: direct
// chats and regular groups that the account has not hidden.
func seedable(chat chatmeta.ChatData) bool {
	if chat.LinkID != 0 || (chat.Meta != nil && chat.Meta.ChatHide) {
		return false
	}
	return chat.Type == "DirectChat" || chat.Type == "MultiChat"
}

// isSeedMessage admits only events that become room messages. A membership or
// metadata feed as a chat's last log describes an earlier state and must not
// be applied as a current change.
func (kc *KakaoClient) isSeedMessage(evt events.Event) bool {
	remote := kc.remoteEventFor(evt)
	return remote != nil && remote.GetType() == bridgev2.RemoteEventMessage
}

// holdInventoryRoom lets a direct room that does not exist yet be created from
// the inventory's display data instead of CHATINFO, MEMLIST and MEMBER
// requests. The returned release must be called once delivery finishes.
func (kc *KakaoClient) holdInventoryRoom(ctx context.Context, chat chatmeta.ChatData) (func(), error) {
	if !inventoryDirectRoom(chat, kc.userID) {
		return func() {}, nil
	}
	if p, err := kc.store.portalByKey(ctx, makePortalKey(chat.ChatID, kc.login.ID)); err != nil {
		return nil, err
	} else if p != nil && p.MXID != "" {
		return func() {}, nil
	}
	kc.cacheMu.Lock()
	if kc.inventoryRooms == nil {
		kc.inventoryRooms = make(map[int64]chatmeta.ChatData)
	}
	kc.inventoryRooms[chat.ChatID] = chat
	kc.cacheMu.Unlock()
	return func() {
		kc.cacheMu.Lock()
		delete(kc.inventoryRooms, chat.ChatID)
		kc.cacheMu.Unlock()
	}, nil
}

// inventoryDirectRoom reports whether chat identifies exactly one other
// participant, which is all a direct room needs.
func inventoryDirectRoom(chat chatmeta.ChatData, self int64) bool {
	return chat.Type == "DirectChat" && len(chat.DisplayUserIDs) == 1 &&
		chat.DisplayUserIDs[0] > 0 && chat.DisplayUserIDs[0] != self
}

// inventoryChatInfo returns the held inventory projection for a direct room
// being created, or nil when the chat is not held.
func (kc *KakaoClient) inventoryChatInfo(portal *bridgev2.Portal, chatID int64) (*bridgev2.ChatInfo, error) {
	kc.cacheMu.Lock()
	chat, ok := kc.inventoryRooms[chatID]
	kc.cacheMu.Unlock()
	if !ok {
		return nil, nil
	}
	// A direct chat's two participants are its complete roster. Its profile
	// is display data rather than a MEMBER record, so it is not cached and
	// fields the inventory omits stay unset rather than cleared.
	info, err := kc.projectChatInfo(portal, chatSnapshot{data: chat, roster: chat.DisplayUserIDs})
	if err != nil {
		return nil, err
	}
	other := makeUserID(chat.DisplayUserIDs[0])
	member := info.Members.MemberMap[other]
	userInfo := &bridgev2.UserInfo{}
	if len(chat.DisplayNicknames) == 1 && chat.DisplayNicknames[0] != "" {
		userInfo.Name = &chat.DisplayNicknames[0]
	}
	if len(chat.DisplayImageURLs) == 1 && chat.DisplayImageURLs[0] != "" {
		userInfo.Avatar = avatarFromURL(chat.DisplayImageURLs[0])
	}
	member.UserInfo = userInfo
	info.Members.MemberMap[other] = member
	return info, nil
}
