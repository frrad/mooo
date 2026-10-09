package connector

import (
	"context"
	"errors"
	"time"

	"github.com/frrad/mooo/internal/protocol/chat"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var commandReconcileGroup = &commands.FullHandler{
	Name:                    "reconcile-group",
	Help:                    commands.HelpMeta{Section: commands.HelpSectionChats, Description: "Bind an unresolved group creation to an explicitly selected Kakao chat", Args: "<Kakao chat ID>"},
	RequiresLoginPermission: true,
	RequiresEventLevel:      event.StateBridge,
	Func: func(ce *commands.Event) {
		if len(ce.Args) != 1 {
			ce.Reply("Usage: $cmdprefix reconcile-group <Kakao chat ID>")
			return
		}
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("No Kakao profile is configured")
			return
		}
		kc, ok := login.Client.(*KakaoClient)
		if !ok || login.UserMXID != ce.User.MXID {
			ce.Reply("Select your own Kakao login before reconciling")
			return
		}
		chatID, err := parseChatID(networkid.PortalID(ce.Args[0]))
		if err != nil {
			ce.Reply("Invalid Kakao chat ID")
			return
		}
		if _, err = kc.ReconcileGroup(ce.Ctx, ce.RoomID, chatID); err != nil {
			ce.Reply("Group reconciliation failed: %v", err)
			return
		}
		ce.Reply("Confirmed source group bound to this room. Reconnect the Kakao login if it was stopped.")
	},
}

// ReconcileGroup never sends CREATE or invitations. The operator explicitly
// selects an existing source room; its roster and Matrix permissions must match
// the original durable attempt before its identity can be recorded.
func (kc *KakaoClient) ReconcileGroup(ctx context.Context, room id.RoomID, chatID int64) (result *bridgev2.CreateChatResponse, resultErr error) {
	return kc.reconcileGroup(ctx, room, chatID, false)
}

// CompleteGroupInvitations explicitly invites only missing original selections
// to the confirmed source group. A durable reservation prevents any retry.
func (kc *KakaoClient) CompleteGroupInvitations(ctx context.Context, room id.RoomID) (*bridgev2.CreateChatResponse, error) {
	return kc.reconcileGroup(ctx, room, 0, true)
}

func (kc *KakaoClient) reconcileGroup(ctx context.Context, room id.RoomID, chatID int64, inviteMissing bool) (result *bridgev2.CreateChatResponse, resultErr error) {
	if ctx == nil || !inviteMissing && chatID <= 0 {
		return nil, errors.New("connector: reconciliation requires a context and positive source chat ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	kc.groupGate.Lock()
	defer kc.groupGate.Unlock()
	meta, err := kc.createMetadata()
	if err != nil {
		return nil, err
	}
	attempt, exists := meta.GroupCreates[string(room)]
	if !exists || attempt.Rejected || attempt.Bound {
		return nil, errors.New("connector: this Matrix room has no unresolved creation to reconcile")
	}
	if inviteMissing {
		if attempt.ChatID <= 0 {
			return nil, errors.New("connector: invitation completion requires a confirmed source group")
		}
		chatID = attempt.ChatID
	}
	if attempt.ChatID != 0 && attempt.ChatID != chatID {
		return nil, errors.New("connector: confirmed source identity cannot be replaced")
	}
	key := makePortalKey(chatID, kc.login.ID)
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: room}
	for _, participant := range attempt.Participants {
		params.Participants = append(params.Participants, makeUserID(participant))
	}
	if _, _, err = kc.groupCreateRequest(params); err != nil {
		return nil, errors.New("connector: saved group participant selection is invalid")
	}
	if err = kc.validateGroupRoom(ctx, params, &key); err != nil {
		return nil, err
	}
	c, err := kc.metadataClient()
	if err != nil {
		kc.mu.Lock()
		busy := kc.stopping || kc.connecting || kc.cleanup != nil
		kc.mu.Unlock()
		if busy || kc.open == nil {
			return nil, errors.New("connector: profile cleanup or connection is still in progress")
		}
		c, err = kc.open()
		if err != nil {
			return nil, err
		}
		defer func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), recoveryCleanupTimeout)
			defer closeCancel()
			if closeErr := c.Shutdown(closeCtx); closeErr != nil {
				kc.mu.Lock()
				if kc.cleanup == nil {
					kc.cleanup = c
				}
				kc.mu.Unlock()
				result = nil
				resultErr = errors.New("connector: reconciliation profile shutdown failed; disconnect the login to finish cleanup")
			}
		}()
		if err = c.Connect(ctx); err != nil {
			return nil, err
		}
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: key}}
	info, err := kc.chatInfoFromClient(ctx, portal, c, true)
	if err != nil {
		return nil, err
	}
	expected := map[networkid.UserID]bool{makeUserID(kc.userID): true}
	for _, participant := range attempt.Participants {
		expected[makeUserID(participant)] = true
	}
	if info.Members == nil || !info.Members.IsFull || !inviteMissing && len(info.Members.MemberMap) != len(expected) {
		return nil, errors.New("connector: selected source room does not match the original roster")
	}
	for participant := range info.Members.MemberMap {
		if !expected[participant] {
			return nil, errors.New("connector: selected source room has an unselected participant")
		}
	}
	if inviteMissing {
		rawRoster, rosterErr := kc.creationSourceRoster(ctx, c, chatID, attempt.Participants)
		if rosterErr != nil {
			return nil, rosterErr
		}
		var missing []int64
		for _, participant := range attempt.Participants {
			if !rawRoster[participant] {
				missing = append(missing, participant)
			}
		}
		if len(missing) > 0 {
			if attempt.InviteAttempted {
				return nil, errors.New("connector: invitation outcome remains incomplete; inspect the source group and reconcile without repeating invitations")
			}
			attempt.InviteAttempted = true
			attempt.Invitees = missing
			if err = kc.saveGroupAttempt(ctx, room, attempt); err != nil {
				return nil, err
			}
			response, inviteErr := c.AddMembers(ctx, chat.AddMembersRequest{ChatID: chatID, MemberIDs: missing})
			if inviteErr != nil {
				return nil, errors.New("connector: invitation did not confirm completion; its reservation is retained and it will not be repeated")
			}
			if response.Warning != "" {
				return nil, errors.New("connector: source returned an invitation warning; inspect the source roster and reconcile without repeating invitations")
			}
		}
	}
	attempt.ChatID = chatID
	if err = kc.saveGroupAttempt(ctx, room, attempt); err != nil {
		return nil, err
	}
	return kc.bindCreatedGroup(ctx, c, room, attempt)
}

var commandCompleteGroupInvitations = &commands.FullHandler{
	Name:                    "complete-group-invitations",
	Help:                    commands.HelpMeta{Section: commands.HelpSectionChats, Description: "Invite missing original selections to this room's confirmed source group once"},
	RequiresLoginPermission: true,
	RequiresEventLevel:      event.StateBridge,
	Func: func(ce *commands.Event) {
		if len(ce.Args) != 0 {
			ce.Reply("Usage: $cmdprefix complete-group-invitations")
			return
		}
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("No Kakao profile is configured")
			return
		}
		kc, ok := login.Client.(*KakaoClient)
		if !ok || login.UserMXID != ce.User.MXID {
			ce.Reply("Select your own Kakao login before completing invitations")
			return
		}
		if _, err := kc.CompleteGroupInvitations(ce.Ctx, ce.RoomID); err != nil {
			ce.Reply("Invitation completion failed: %v", err)
			return
		}
		ce.Reply("Confirmed source group with the complete selected roster bound to this room. Reconnect the Kakao login if it was stopped.")
	},
}
