package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// discoverGroups uses the completed full login inventory, never a resumed
// delta, and leaves messages and read watermarks to the existing catch-up path.
func (kc *KakaoClient) discoverGroups(ctx context.Context, c kakaoClient) error {
	chats, err := c.ListChats(ctx)
	if err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(chats))
	ordered := make([]int64, 0, len(chats))
	for _, chat := range chats {
		if chat.Type != "MultiChat" || chat.LinkID != 0 {
			continue
		}
		if chat.ChatID <= 0 {
			return errChatInfoMismatch
		}
		if _, exists := seen[chat.ChatID]; exists {
			continue
		}
		seen[chat.ChatID] = struct{}{}
		ordered = append(ordered, chat.ChatID)
	}
	// Revoke absent groups before a present room refresh can fail. The complete
	// inventory has already been validated, so absence is authoritative.
	if err = kc.reconcileMissingGroups(ctx, seen); err != nil {
		return err
	}
	for _, chatID := range ordered {
		if err = kc.refreshGroupMembership(ctx, c, chatID, true); err != nil {
			return err
		}
	}
	return nil
}

// Validate creation metadata before setting CreatePortal: bridgev2 may fall back
// to NetworkAPI.GetChatInfo if an event provider fails during room creation.
func (kc *KakaoClient) prepareMemberDiscovery(ctx context.Context, c chatMetaAPI, chatID int64, evt *chatInfoChangeEvent) error {
	key := makePortalKey(chatID, kc.login.ID)
	if p, err := kc.store.portalByKey(ctx, key); err != nil {
		return err
	} else if p != nil && p.MXID != "" {
		return nil
	}
	p := &bridgev2.Portal{Portal: &database.Portal{PortalKey: key}}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, _, err := kc.chatInfoFromClient(ctx, p, c, chatInfoOptions{regularGroupOnly: true})
	if err != nil {
		return err
	}
	evt.CreatePortal = true
	evt.getCreationInfo = func(_ context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
		if portal == nil || portal.PortalKey != key {
			return nil, errChatInfoMismatch
		}
		return info, nil
	}
	return nil
}
