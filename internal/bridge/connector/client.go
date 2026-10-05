package connector

import (
	"context"
	"errors"
	"fmt"
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
	SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error)
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
}

var (
	_ bridgev2.NetworkAPI           = (*KakaoClient)(nil)
	_ bridgev2.NetworkAPIWithUserID = (*KakaoClient)(nil)
)

func newKakaoClient(login *bridgev2.UserLogin, userID int64, open func() (kakaoClient, error)) *KakaoClient {
	kc := &KakaoClient{
		login:  login,
		userID: userID,
		open:   open,
		queue:  login.QueueRemoteEvent,
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
	stream, err := kc.connectAndSubscribe(ctx, c)
	if err != nil {
		kc.mu.Lock()
		kc.cleanup = c
		kc.cleanupDone = nil
		kc.mu.Unlock()
		if shutdownErr := shutdownKakaoClient(c); shutdownErr != nil {
			kc.log().Err(shutdownErr).Msg("Failed to clean up Kakao client after connect failure")
		} else {
			kc.mu.Lock()
			if kc.cleanup == c {
				kc.cleanup = nil
				kc.cleanupDone = nil
			}
			kc.mu.Unlock()
		}
		kc.log().Err(err).Msg("Failed to connect to KakaoTalk")
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateConnectFailed})
		return
	}
	done := make(chan struct{})
	kc.mu.Lock()
	if kc.stopping {
		kc.mu.Unlock()
		kc.mu.Lock()
		kc.cleanup = c
		kc.cleanupDone = nil
		kc.mu.Unlock()
		if shutdownErr := shutdownKakaoClient(c); shutdownErr != nil {
			kc.log().Err(shutdownErr).Msg("Failed to clean up stopped Kakao client")
		} else {
			kc.mu.Lock()
			if kc.cleanup == c {
				kc.cleanup = nil
				kc.cleanupDone = nil
			}
			kc.mu.Unlock()
		}
		return
	}
	kc.client = c
	kc.done = done
	kc.mu.Unlock()
	kc.sendState(status.BridgeState{StateEvent: status.StateConnected})
	go kc.run(c, stream, done)
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

// GetChatInfo returns placeholder metadata. The client has no chat-info or
// member-list API yet (bridge plan phase B2).
func (kc *KakaoClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	chatID, err := parseChatID(portal.ID)
	if err != nil {
		return nil, err
	}
	name := placeholderChatName(chatID)
	return &bridgev2.ChatInfo{
		Name: &name,
		Members: &bridgev2.ChatMemberList{
			MemberMap: bridgev2.ChatMemberMap{}.Set(bridgev2.ChatMember{
				EventSender: kc.selfSender(),
				Membership:  event.MembershipJoin,
			}),
		},
	}, nil
}

// GetUserInfo returns a placeholder profile until profile sync exists.
func (kc *KakaoClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	userID, err := parseUserID(string(ghost.ID))
	if err != nil {
		return nil, err
	}
	name := placeholderUserName(userID)
	return &bridgev2.UserInfo{Name: &name}, nil
}

func (kc *KakaoClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID:            "com.github.frrad.mooo.capabilities.2026_09_30",
		MaxTextLength: maxTextLength,
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
	response, err := c.SendText(ctx, chatID, body)
	if err != nil {
		return nil, err
	}
	if response.LogID <= 0 {
		return nil, errors.New("KakaoTalk accepted the message without a log ID")
	}
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        makeMessageID(chatID, response.LogID),
			SenderID:  makeUserID(kc.userID),
			Timestamp: kakaoTime(response.SendAt),
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
