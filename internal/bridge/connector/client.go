package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// kakaoClient is the subset of client.Client the connector uses. It exists so
// the event loop and send paths can be tested without a Kakao backend.
type kakaoClient interface {
	Connect(ctx context.Context) error
	Events(ctx context.Context) (<-chan events.Result, error)
	CommitEvent(event events.Event) error
	ResumeTargets(ctx context.Context) ([]syncmsg.Target, error)
	CatchUp(ctx context.Context, chatID, targetMax int64) ([]events.Event, error)
	ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error)
	Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error)
	MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error)
	SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error)
	SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error)
	Close() error
	Shutdown(ctx context.Context) error
}

func openProfileClient(statePath string) (kakaoClient, error) {
	return client.Open(statePath, nil)
}

// Bridge state error codes reported by this connector.
const (
	stateProfileUnavailable status.BridgeStateErrorCode = "kakao-profile-unavailable"
	stateConnectFailed      status.BridgeStateErrorCode = "kakao-connect-failed"
	stateDisconnected       status.BridgeStateErrorCode = "kakao-disconnected"
	stateKickedOut          status.BridgeStateErrorCode = "kakao-kicked-out"
	stateChangeServer       status.BridgeStateErrorCode = "kakao-change-server"
)

var terminalDisconnectTimeout = 5 * time.Second

var errUnsupportedOpenChatMetadata = errors.New("connector: OpenChat metadata is not supported")
var errChatInfoMismatch = errors.New("connector: CHATINFO returned a different chat ID")
var errInvalidMemberRoster = errors.New("connector: MEMLIST returned an invalid member ID")

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		stateProfileUnavailable: "The Kakao profile could not be opened; it may be in use by another process.",
		stateConnectFailed:      "Connecting to KakaoTalk failed.",
		stateDisconnected:       "The KakaoTalk session ended. Restart the bridge to reconnect.",
		stateKickedOut:          "KakaoTalk ended this device's session.",
		stateChangeServer:       "KakaoTalk requested a server change. Restart the bridge to reconnect.",
	})
}

// KakaoClient is the bridgev2 NetworkAPI for one Kakao profile. It owns the
// profile's client.Client, and with it the profile lease and the continuity
// checkpoint, for as long as the login is connected.
//
// Inbound message events are handed to the bridge one at a time, in delivery
// order, and committed to the checkpoint only once the bridge reports them
// handled. The client never reconnects on its own; a lost session is reported
// through bridge state and left for an explicit restart.
type KakaoClient struct {
	login  *bridgev2.UserLogin
	userID int64
	open   func() (kakaoClient, error)

	// queue and sendState default to the login's bridge methods and are
	// replaced in tests.
	queue     func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult
	sendState func(status.BridgeState)

	mu             sync.Mutex
	disconnectGate chan struct{}
	client         kakaoClient
	cleanup        kakaoClient
	connecting     bool
	stopping       bool
	done           chan struct{}
	cleanupDone    chan struct{}
	profiles       map[int64]chatmeta.Member
}

var (
	_ bridgev2.NetworkAPI           = (*KakaoClient)(nil)
	_ bridgev2.NetworkAPIWithUserID = (*KakaoClient)(nil)
)

func newKakaoClient(login *bridgev2.UserLogin, userID int64, open func() (kakaoClient, error)) *KakaoClient {
	kc := &KakaoClient{
		login:    login,
		userID:   userID,
		open:     open,
		queue:    login.QueueRemoteEvent,
		profiles: make(map[int64]chatmeta.Member),
	}
	kc.sendState = func(state status.BridgeState) { kc.stateQueue().Send(state) }
	return kc
}

// stateQueue looks up the login's bridge-state queue at send time. The
// framework loads a stored login by calling LoadUserLogin before it creates
// the queue, so binding it at construction would capture nil and silently
// drop every state.
func (kc *KakaoClient) stateQueue() *bridgev2.BridgeStateQueue {
	return kc.login.BridgeState
}

func (kc *KakaoClient) log() *zerolog.Logger {
	return &kc.login.Log
}

func (kc *KakaoClient) Connect(ctx context.Context) {
	kc.mu.Lock()
	if kc.client != nil || kc.cleanup != nil || kc.connecting {
		kc.mu.Unlock()
		return
	}
	kc.connecting = true
	kc.stopping = false
	kc.mu.Unlock()
	defer func() {
		kc.mu.Lock()
		kc.connecting = false
		kc.mu.Unlock()
	}()

	kc.sendState(status.BridgeState{StateEvent: status.StateConnecting})
	c, err := kc.open()
	if err != nil {
		kc.log().Err(err).Msg("Failed to open Kakao profile")
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateProfileUnavailable})
		return
	}
	// Retain the bootstrap owner before any potentially blocking Connect,
	// catch-up, or Events call. Disconnect must be able to interrupt and join a
	// client whose subscription has not returned yet.
	kc.mu.Lock()
	kc.cleanup = c
	kc.cleanupDone = nil
	kc.mu.Unlock()
	stream, err := kc.connectAndSubscribe(ctx, c)
	if err != nil {
		kc.shutdownBootstrap(c, "after connect failure", false)
		kc.log().Err(err).Msg("Failed to connect to KakaoTalk")
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateConnectFailed})
		return
	}
	done := make(chan struct{})
	kc.mu.Lock()
	if kc.stopping {
		kc.mu.Unlock()
		kc.shutdownBootstrap(c, "stopped client", true)
		return
	}
	kc.client = c
	kc.done = done
	kc.mu.Unlock()
	kc.sendState(status.BridgeState{StateEvent: status.StateConnected})
	go kc.run(c, stream, done)
}

// shutdownBootstrap releases an owner retained before bootstrap calls. A
// concurrent Disconnect owns cleanup once it marks stopping, so Connect must
// leave that shutdown and any retry to Disconnect.
func (kc *KakaoClient) shutdownBootstrap(c kakaoClient, phase string, force bool) {
	kc.mu.Lock()
	owned := kc.cleanup == c && (force || !kc.stopping)
	kc.mu.Unlock()
	if !owned {
		return
	}
	if err := shutdownKakaoClient(c); err != nil {
		kc.log().Err(err).Msg("Failed to clean up Kakao client " + phase)
		return
	}
	kc.mu.Lock()
	if kc.cleanup == c {
		kc.cleanup = nil
		kc.cleanupDone = nil
	}
	kc.mu.Unlock()
}

func shutdownKakaoClient(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), terminalDisconnectTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}

// connectAndSubscribe logs in, recovers what was missed while disconnected,
// and only then subscribes to live events. The order matters: the client
// commits strictly in delivery order per chat, and a live message committed
// first would move the chat's cursor past the missed ones. Until Events is
// called the session buffers pushes, so nothing live is lost meanwhile.
func (kc *KakaoClient) connectAndSubscribe(ctx context.Context, c kakaoClient) (<-chan events.Result, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	if err := kc.catchUp(ctx, c); err != nil {
		return nil, err
	}
	return c.Events(ctx)
}

// catchUp bridges and commits, in order, the messages each previously
// bridged chat received while the bridge was away. SYNCMSG may mark them read
// on the server. A chat whose interval cannot be recovered keeps its recorded
// gap and gets a notice; any other failure aborts the connection so live
// commits never skip past unrecovered messages.
func (kc *KakaoClient) catchUp(ctx context.Context, c kakaoClient) error {
	targets, err := c.ResumeTargets(ctx)
	if err != nil {
		return fmt.Errorf("list catch-up targets: %w", err)
	}
	for _, target := range targets {
		missed, err := c.CatchUp(ctx, target.ChatID, target.MaxLogID)
		if errors.Is(err, client.ErrGapUnresolved) {
			kc.log().Warn().Int64("kakao_chat_id", target.ChatID).Msg("Could not recover messages missed while disconnected")
			kc.queue(kc.gapNotice(target.ChatID, target.MaxLogID))
			continue
		} else if err != nil {
			return fmt.Errorf("catch up chat: %w", err)
		}
		for _, evt := range missed {
			kc.handleEvent(c, evt)
		}
	}
	return nil
}

// run consumes the typed event stream until the session ends. It is the only
// goroutine that queues remote events or commits, which preserves the
// client's per-chat commit order.
func (kc *KakaoClient) run(c kakaoClient, stream <-chan events.Result, done chan struct{}) {
	defer close(done)
	kickedOut := false
	changeServer := false
	for result := range stream {
		if kickedOut || changeServer {
			// A terminal notice ends the session's event acceptance window. The
			// stream still has to close so the owner can publish its terminal
			// bridge state, but later packets must not be committed.
			continue
		}
		if result.Err != nil {
			kc.log().Warn().Err(result.Err).Msg("Dropped undecodable Kakao event")
			continue
		}
		if _, ok := result.Event.(events.Kickout); ok {
			kickedOut = true
		}
		if _, ok := result.Event.(events.ChangeServer); ok {
			changeServer = true
		}
		kc.handleEvent(c, result.Event)
	}
	kc.mu.Lock()
	stopping := kc.stopping
	kc.mu.Unlock()
	switch {
	case stopping:
	case kickedOut:
		kc.sendState(status.BridgeState{StateEvent: status.StateBadCredentials, Error: stateKickedOut})
	case changeServer:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateChangeServer})
	default:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDisconnected})
	}
}

func (kc *KakaoClient) handleEvent(c kakaoClient, evt events.Event) {
	remote := kc.remoteEventFor(evt)
	if remote == nil {
		kc.log().Debug().Str("kind", string(evt.Kind())).Msg("Ignoring Kakao event not bridged yet")
		return
	}
	result := kc.queue(remote)
	if !committable(result) {
		kc.log().Warn().Err(result.Error).
			Bool("success", result.Success).
			Bool("queued", result.Queued).
			Msg("Kakao message was not confirmed as bridged; leaving it uncommitted for replay")
		return
	}
	if err := c.CommitEvent(evt); err != nil {
		kc.log().Err(err).Msg("Failed to commit bridged Kakao message")
	}
}

// committable reports whether the bridge finished handling an event. Ignored
// events (duplicates, filtered portals) count as handled. A queued result,
// including one returned because handling was backgrounded after a timeout,
// does not.
func committable(result bridgev2.EventHandlingResult) bool {
	return result.Success && !result.Queued && result.Error == nil
}

func (kc *KakaoClient) Disconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), terminalDisconnectTimeout)
	defer cancel()
	kc.mu.Lock()
	if kc.disconnectGate == nil {
		kc.disconnectGate = make(chan struct{}, 1)
	}
	gate := kc.disconnectGate
	kc.mu.Unlock()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
		if err := ctx.Err(); err != nil {
			return
		}
	case <-ctx.Done():
		return
	}
	kc.mu.Lock()
	c := kc.client
	done := kc.done
	if c == nil {
		c = kc.cleanup
		done = kc.cleanupDone
	}
	kc.stopping = true
	kc.client = nil
	kc.done = nil
	if c != nil {
		kc.cleanup = c
		kc.cleanupDone = done
	}
	kc.mu.Unlock()
	if c == nil {
		return
	}
	err := c.Shutdown(ctx)
	deadline, hasDeadline := ctx.Deadline()
	if err == nil && done != nil {
		wait := time.Until(deadline)
		if !hasDeadline || wait <= 0 {
			err = context.DeadlineExceeded
		} else {
			timer := time.NewTimer(wait)
			select {
			case <-done:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
				err = context.DeadlineExceeded
			}
		}
	}
	if err != nil {
		kc.log().Err(err).Msg("Failed to close Kakao client")
		return
	}
	kc.mu.Lock()
	if kc.cleanup == c {
		kc.cleanup = nil
		kc.cleanupDone = nil
	}
	kc.mu.Unlock()
}

func (kc *KakaoClient) IsLoggedIn() bool {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	return kc.client != nil
}

// LogoutRemote only disconnects. Revoking the device is a primary-device
// action that the bridge does not perform.
func (kc *KakaoClient) LogoutRemote(ctx context.Context) {
	kc.Disconnect()
}

func (kc *KakaoClient) GetUserID() networkid.UserID {
	return makeUserID(kc.userID)
}

func (kc *KakaoClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return userID == makeUserID(kc.userID)
}

// GetChatInfo resolves metadata from CHATINFO, MEMLIST, and MEMBER. The
// profile APIs are deliberately called only once per request; the bridge does
// not invent names or avatars when Kakao omits them.
func (kc *KakaoClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	chatID, err := parseChatID(portal.ID)
	if err != nil {
		return nil, err
	}
	c, err := kc.metadataClient()
	if err != nil {
		return nil, err
	}
	response, err := c.ChatInfo(ctx, chatID)
	if err != nil {
		return nil, err
	}
	data := response.ChatData
	if data.ChatID != chatID {
		return nil, fmt.Errorf("%w: requested %d, got %d", errChatInfoMismatch, chatID, data.ChatID)
	}
	if data.LinkID > 0 {
		return nil, errUnsupportedOpenChatMetadata
	}

	// MEMLIST is a UI-originated API in the official client and its stored
	// room token is not exposed by ChatData. An initial bridge sync therefore
	// starts at token zero; later membership updates remain separate work.
	roster, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
		return nil, err
	}
	completeRoster := len(roster.MemberIDs) > 0
	for _, userID := range roster.MemberIDs {
		if userID <= 0 {
			return nil, fmt.Errorf("%w: %d", errInvalidMemberRoster, userID)
		}
	}
	userIDs := append([]int64(nil), roster.MemberIDs...)
	if len(userIDs) == 0 {
		userIDs = append(userIDs, data.DisplayUserIDs...)
	}
	profiles, err := c.Members(ctx, chatID, userIDs)
	if err != nil {
		return nil, err
	}

	members := bridgev2.ChatMemberMap{}.Set(bridgev2.ChatMember{
		EventSender: kc.selfSender(),
		Membership:  event.MembershipJoin,
	})
	requested := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		requested[userID] = struct{}{}
	}
	for _, profile := range profiles {
		if profile.UserID <= 0 || profile.UserID == kc.userID {
			continue
		}
		if _, ok := requested[profile.UserID]; !ok {
			continue
		}
		kc.mu.Lock()
		kc.profiles[profile.UserID] = profile
		kc.mu.Unlock()
		member := bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{Sender: makeUserID(profile.UserID)},
			Membership:  event.MembershipJoin,
			UserInfo:    userInfoForMember(profile),
		}
		members.Set(member)
	}
	// If MEMBER returned no profile for an ID, retain the source-supported
	// membership identity without fabricating profile fields.
	for _, userID := range userIDs {
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

	info := &bridgev2.ChatInfo{Members: &bridgev2.ChatMemberList{
		IsFull:    completeRoster,
		MemberMap: members,
	}}
	if completeRoster {
		info.Members.TotalMemberCount = len(members)
	} else if data.ActiveMemberCount > 0 {
		info.Members.TotalMemberCount = int(data.ActiveMemberCount)
	}
	if name := chatName(data); name != "" {
		info.Name = &name
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
	return info, nil
}

// GetUserInfo returns a profile previously resolved in a room. MEMBER is
// chat-scoped, so an uncached ghost has no source-supported name to return.
func (kc *KakaoClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	userID, err := parseUserID(string(ghost.ID))
	if err != nil {
		return nil, err
	}
	kc.mu.Lock()
	profile, ok := kc.profiles[userID]
	kc.mu.Unlock()
	if !ok {
		return &bridgev2.UserInfo{}, nil
	}
	return userInfoForMember(profile), nil
}

func (kc *KakaoClient) metadataClient() (kakaoClient, error) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.stopping {
		return nil, bridgev2.ErrNotLoggedIn
	}
	if kc.client != nil {
		return kc.client, nil
	}
	if kc.connecting && kc.cleanup != nil {
		return kc.cleanup, nil
	}
	return nil, bridgev2.ErrNotLoggedIn
}

func chatName(data chatmeta.ChatData) string {
	if data.Meta != nil && data.Meta.Name != "" {
		return data.Meta.Name
	}
	names := make([]string, 0, len(data.DisplayNicknames))
	for _, nickname := range data.DisplayNicknames {
		if nickname != "" {
			names = append(names, nickname)
		}
	}
	return strings.Join(names, ", ")
}

func userInfoForMember(profile chatmeta.Member) *bridgev2.UserInfo {
	info := &bridgev2.UserInfo{}
	if profile.Nickname != "" {
		name := profile.Nickname
		info.Name = &name
	}
	return info
}

func (kc *KakaoClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID:            "com.github.frrad.mooo.capabilities.2026_10_04",
		MaxTextLength: maxTextLength,
		Reply:         event.CapLevelPartialSupport,
	}
}

// maxTextLength is a conservative bound; the official limit is not yet
// established.
const maxTextLength = 10000

// HandleMatrixMessage sends one plain text message. A failed or ambiguous
// send is reported to Matrix and never retried.
func (kc *KakaoClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
	default:
		return nil, bridgev2.ErrUnsupportedMessageType
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return nil, err
	}
	if msg.ReplyTo == nil && msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplyTo() != "" {
		return nil, errMissingReplyMetadata
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	body := msg.Content.Body
	if msg.Content.MsgType == event.MsgEmote {
		body = "* " + body
	}
	var response chat.WriteResponse
	if msg.ReplyTo != nil {
		target, err := replyTargetFor(msg.ReplyTo, msg.Portal.PortalKey)
		if err != nil {
			return nil, err
		}
		response, err = c.SendReply(ctx, chat.ReplyRequest{ChatID: chatID, Message: body, Target: target})
		if err != nil {
			return nil, err
		}
	} else {
		response, err = c.SendText(ctx, chatID, body)
		if err != nil {
			return nil, err
		}
	}
	if response.LogID <= 0 {
		return nil, errors.New("KakaoTalk accepted the message without a log ID")
	}
	sentType := chat.TextType
	if msg.ReplyTo != nil {
		sentType = chat.ReplyType
	}
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        makeMessageID(chatID, response.LogID),
			SenderID:  makeUserID(kc.userID),
			Timestamp: kakaoTime(response.SendAt),
			Metadata:  newKakaoMessageMetadata(chatID, response.LogID, kc.userID, sentType, body, 0),
		},
	}, nil
}

func (kc *KakaoClient) selfSender() bridgev2.EventSender {
	return bridgev2.EventSender{
		IsFromMe:    true,
		SenderLogin: kc.login.ID,
		Sender:      makeUserID(kc.userID),
	}
}
