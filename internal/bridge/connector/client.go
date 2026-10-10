package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	bridgematrix "maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// kakaoClient is the subset of client.Client the connector uses. It exists so
// the event loop and send paths can be tested without a Kakao backend.
type kakaoClient interface {
	Connect(ctx context.Context) error
	CreateChat(context.Context, chat.CreateRequest) (chat.CreateResponse, error)
	AddMembers(context.Context, chat.AddMembersRequest) (chat.AddMembersResponse, error)
	ListChats(ctx context.Context) ([]chatmeta.ChatData, error)
	Events(ctx context.Context) (<-chan events.Result, error)
	CommitEvent(event events.Event) error
	ResumeTargets(ctx context.Context) ([]syncmsg.Target, error)
	CatchUp(ctx context.Context, chatID, targetMax int64) ([]events.Event, error)
	ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error)
	PersonalMeta(ctx context.Context, chatID int64) (*chatmeta.RoomMeta, error)
	MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error)
	Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error)
	MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error)
	SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error)
	SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error)
	SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error)
	MarkRead(ctx context.Context, chatID, watermark int64) (syncmsg.Response, error)
	Close() error
	Shutdown(ctx context.Context) error
}

type bootstrapFailure struct {
	stage string
	err   error
}

func (e bootstrapFailure) Error() string { return e.stage + ": " + e.err.Error() }
func (e bootstrapFailure) Unwrap() error { return e.err }

func openProfileClient(statePath string) (kakaoClient, error) {
	return client.OpenWithOptions(statePath, nil, client.OpenOptions{FullChatList: true})
}

// Bridge state error codes reported by this connector.
const (
	stateProfileUnavailable    status.BridgeStateErrorCode = "kakao-profile-unavailable"
	stateConnectFailed         status.BridgeStateErrorCode = "kakao-connect-failed"
	stateGroupCreateUnresolved status.BridgeStateErrorCode = "kakao-group-create-unresolved"
	stateDisconnected          status.BridgeStateErrorCode = "kakao-disconnected"
	stateDeliveryPaused        status.BridgeStateErrorCode = "kakao-delivery-paused"
	stateUnidentifiableMsg     status.BridgeStateErrorCode = "kakao-unidentifiable-message"
	stateKickedOut             status.BridgeStateErrorCode = "kakao-kicked-out"
	stateChangeServer          status.BridgeStateErrorCode = "kakao-change-server"
)

var terminalDisconnectTimeout = 5 * time.Second

const maxRecoveryAttempts = 5
const recoveryCleanupTimeout = 5 * time.Second

var ordinaryRecoveryDelays = [...]time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second}
var rateLimitedRecoveryDelays = [...]time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second, 480 * time.Second, 900 * time.Second}

var errUnsupportedOpenChatMetadata = errors.New("connector: OpenChat metadata is not supported")
var errChatInfoMismatch = errors.New("connector: CHATINFO returned a different chat ID")
var errInvalidMemberRoster = errors.New("connector: MEMLIST returned an invalid member ID")
var errUnsupportedImageReply = errors.New("connector: image replies are not supported")

// permanentAdmissionError marks a continuity failure that cannot be safely
// recovered by reconnecting. The owner is still cleaned up, but no new
// session is opened until an operator intervenes.
type permanentAdmissionError struct{ err error }

func (e permanentAdmissionError) Error() string { return e.err.Error() }
func (e permanentAdmissionError) Unwrap() error { return e.err }

func unidentifiableMessageError() error {
	return permanentAdmissionError{err: events.ErrUnidentifiableMessage}
}

func isUnidentifiableMessageError(err error) bool {
	return errors.Is(err, events.ErrUnidentifiableMessage)
}

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		stateProfileUnavailable:    "The Kakao profile could not be opened; it may be in use by another process.",
		stateConnectFailed:         "Connecting to KakaoTalk failed.",
		stateGroupCreateUnresolved: "A group creation outcome or room binding is unresolved. Reconcile the selected Matrix room with the source group; do not repeat creation.",
		stateDisconnected:          "The KakaoTalk session ended. Restart the bridge to reconnect.",
		stateDeliveryPaused:        "Inbound Kakao delivery was not confirmed. Progress was retained; bounded reconnect will attempt replay. Restore Matrix/source access and reconnect explicitly if recovery stops.",
		stateUnidentifiableMsg:     "Kakao delivered a message whose identity could not be established safely. Review the bridge logs and repair continuity before reconnecting.",
		stateKickedOut:             "KakaoTalk ended this device's session.",
		stateChangeServer:          "KakaoTalk requested a server change. Restart the bridge to reconnect.",
	})
}

// KakaoClient is the bridgev2 NetworkAPI for one Kakao profile. It owns the
// profile's client.Client, and with it the profile lease and the continuity
// checkpoint, for as long as the login is connected.
//
// Inbound message events are handed to the bridge one at a time, in delivery
// order, and committed to the checkpoint only once the bridge reports them
// handled. The protocol client never reconnects on its own; recovery is an
// explicit connector policy that never
// changes the protocol client's own lifecycle or retries outbound mutations.
type KakaoClient struct {
	login  *bridgev2.UserLogin
	userID int64
	open   func() (kakaoClient, error)

	// queue and sendState default to the login's bridge methods and are
	// replaced in tests.
	queue     func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult
	sendState func(status.BridgeState)
	// reserveOutbound defaults to the bridge's durable store.
	reserveOutbound func(context.Context, id.EventID) (bool, error)

	groupGate      sync.Mutex
	displayGate    sync.Mutex
	sourceBlocked  map[int64]error
	mu             sync.Mutex
	disconnectGate chan struct{}
	client         kakaoClient
	cleanup        kakaoClient
	connecting     bool
	stopping       bool
	done           chan struct{}
	// cleanupPumpDone remains associated with cleanup while a Disconnect
	// timeout leaves the event pump running. It must not be discarded when
	// the active client fields are cleared: a later cleanup attempt must join
	// the same generation before releasing the profile owner.
	cleanupPumpDone      chan struct{}
	cleanupDone          chan struct{}
	cleanupBusy          bool
	cleanupRetryCancel   context.CancelFunc
	cleanupRetryID       uint64
	cleanupRetryAttempts int
	cleanupRetryDone     chan struct{}
	connectCancel        context.CancelFunc
	// lifecycle spans one Connect..Disconnect cycle, including bounded
	// recovery reconnects. Disconnect cancels it before waiting for the event
	// pump, which interrupts in-flight inbound conversions.
	lifecycle            context.Context
	lifecycleCancel      context.CancelFunc
	retryCancel          context.CancelFunc
	retryDone            chan struct{}
	retryID              uint64
	generation           uint64
	connectingGeneration uint64
	recoveryTry          int
	wait                 func(context.Context, time.Duration) error
	profiles             map[int64]chatmeta.Member
	profileRefreshAfter  string
	reactionMu           sync.Mutex
	reactionRevisions    map[string]int64
	reactionNoticeMu     sync.Mutex
	reactionNotices      map[string]time.Time
}

var (
	_ bridgev2.NetworkAPI           = (*KakaoClient)(nil)
	_ bridgev2.NetworkAPIWithUserID = (*KakaoClient)(nil)
)

func newKakaoClient(login *bridgev2.UserLogin, userID int64, open func() (kakaoClient, error)) *KakaoClient {
	kc := &KakaoClient{
		login:             login,
		userID:            userID,
		open:              open,
		queue:             login.QueueRemoteEvent,
		profiles:          make(map[int64]chatmeta.Member),
		reactionRevisions: make(map[string]int64),
		reactionNotices:   make(map[string]time.Time),
	}
	kc.wait = waitForRecovery
	kc.reserveOutbound = kc.reserveOutboundKV
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
	if kc.lifecycle == nil {
		kc.lifecycle, kc.lifecycleCancel = context.WithCancel(context.Background())
	}
	kc.generation++
	generation := kc.generation
	kc.connectingGeneration = generation
	connectCtx, cancel := context.WithCancel(ctx)
	kc.connectCancel = cancel
	kc.mu.Unlock()
	defer func() {
		cancel()
		kc.mu.Lock()
		if kc.generation == generation || (kc.connecting && kc.connectingGeneration == generation) {
			kc.connecting = false
			kc.connectCancel = nil
		}
		kc.mu.Unlock()
	}()

	kc.sendState(status.BridgeState{StateEvent: status.StateConnecting})
	kc.connectOnce(connectCtx, generation, false)
}

func (kc *KakaoClient) connectOnce(ctx context.Context, generation uint64, recovering bool) {
	c, err := kc.open()
	if err != nil {
		kc.log().Err(err).Msg("Failed to open Kakao profile")
		if kc.isCurrent(generation, ctx) {
			if !recovering {
				kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateProfileUnavailable})
			} else {
				kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateConnectFailed})
			}
		}
		return
	}
	if !kc.isCurrent(generation, ctx) {
		kc.mu.Lock()
		if kc.cleanup == nil {
			kc.cleanup = c
			kc.cleanupPumpDone = nil
			kc.cleanupDone = nil
			kc.cleanupBusy = false
		}
		kc.mu.Unlock()
		kc.shutdownBootstrap(c, "stale bootstrap", true)
		return
	}
	// Retain the bootstrap owner before any potentially blocking Connect,
	// catch-up, or Events call. Disconnect must be able to interrupt and join a
	// client whose subscription has not returned yet.
	kc.mu.Lock()
	kc.cleanup = c
	kc.cleanupPumpDone = nil
	kc.cleanupDone = nil
	kc.cleanupBusy = false
	kc.mu.Unlock()
	stream, err := kc.connectAndSubscribe(ctx, c)
	if err != nil {
		kc.shutdownBootstrap(c, "after connect failure", false)
		kc.log().Err(err).Msg("Failed to connect to KakaoTalk")
		if errors.Is(err, errSourceAccessRemoved) {
			if kc.isCurrent(generation, ctx) {
				kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupAccessRemoved})
			}
			return
		}
		if errors.Is(err, errMembershipPending) {
			if kc.isCurrent(generation, ctx) {
				kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupMembershipPending})
			}
			return
		}
		if errors.Is(err, errGroupCreateUnresolved) {
			if kc.isCurrent(generation, ctx) {
				kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupCreateUnresolved})
			}
			return
		}
		if isUnidentifiableMessageError(err) {
			if kc.isCurrent(generation, ctx) {
				kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateUnidentifiableMsg})
			}
			return
		}
		if recovering && retryableRecoveryError(err) && kc.retryAfter(err, generation) {
			if errors.Is(err, errDeliveryNotConfirmed) && kc.isCurrent(generation, ctx) {
				kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDeliveryPaused})
			}
			return
		}
		if kc.isCurrent(generation, ctx) {
			kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateConnectFailed})
		}
		return
	}
	done := make(chan struct{})
	kc.mu.Lock()
	if kc.stopping || kc.generation != generation || kc.cleanup != c {
		kc.mu.Unlock()
		kc.shutdownBootstrap(c, "stopped client", true)
		return
	}
	kc.client = c
	kc.done = done
	kc.recoveryTry = 0
	kc.cleanupRetryAttempts = 0
	kc.mu.Unlock()
	kc.sendReadyState()
	go kc.run(c, stream, done, generation)
}

// connectionLifecycle returns the current connection lifecycle, or nil when
// the client is not between Connect and Disconnect.
func (kc *KakaoClient) connectionLifecycle() context.Context {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	return kc.lifecycle
}

func (kc *KakaoClient) isCurrent(generation uint64, ctx context.Context) bool {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	return kc.generation == generation && !kc.stopping && ctx.Err() == nil
}

func waitForRecovery(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (kc *KakaoClient) retryAfter(err error, generation uint64) bool {
	if !retryableRecoveryError(err) {
		return false
	}
	kc.mu.Lock()
	if kc.stopping || kc.generation != generation || kc.recoveryTry >= maxRecoveryAttempts {
		kc.mu.Unlock()
		return false
	}
	attempt := kc.recoveryTry
	kc.recoveryTry++
	ctx, cancel := context.WithCancel(context.Background())
	kc.retryID++
	retryID := kc.retryID
	kc.retryCancel = cancel
	kc.retryDone = make(chan struct{})
	retryDone := kc.retryDone
	wait := kc.wait
	delay := recoveryDelay(err, attempt)
	kc.mu.Unlock()
	go func() {
		defer func() {
			close(retryDone)
			kc.mu.Lock()
			if kc.retryID == retryID {
				kc.retryCancel = nil
				kc.retryDone = nil
			}
			kc.mu.Unlock()
		}()
		if err := wait(ctx, delay); err != nil {
			return
		}
		kc.mu.Lock()
		if kc.stopping || kc.generation != generation || kc.client != nil || kc.cleanup != nil || kc.retryID != retryID {
			kc.mu.Unlock()
			return
		}
		kc.connecting = true
		kc.connectingGeneration = generation
		connectCtx, connectCancel := context.WithCancel(ctx)
		kc.connectCancel = connectCancel
		kc.mu.Unlock()
		kc.sendState(status.BridgeState{StateEvent: status.StateConnecting})
		kc.connectOnce(connectCtx, generation, true)
		connectCancel()
		kc.mu.Lock()
		if kc.generation == generation {
			kc.connecting = false
			kc.connectCancel = nil
		}
		kc.mu.Unlock()
	}()
	return true
}

func recoveryDelay(err error, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= maxRecoveryAttempts {
		attempt = maxRecoveryAttempts - 1
	}
	var delays = ordinaryRecoveryDelays[:]
	var statusErr client.StatusError
	if errors.As(err, &statusErr) && statusErr.Status == -328 {
		delays = rateLimitedRecoveryDelays[:]
	}
	return delays[attempt]
}

// errDeliveryNotConfirmed marks a catch-up stopped because Matrix did not
// confirm an event; its source progress is retained for replay.
var errDeliveryNotConfirmed = errors.New("connector: delivery not confirmed")

func retryableRecoveryError(err error) bool {
	if err == nil || errors.Is(err, client.ErrLogin) || errors.Is(err, client.ErrCredentialRenewal) {
		return false
	}
	var bootstrapErr bootstrapFailure
	if errors.As(err, &bootstrapErr) {
		if bootstrapErr.stage == "catch-up" {
			// Only Matrix-side delivery is retried here; source failures during
			// catch-up stay terminal. Progress was retained, so replay is safe.
			return errors.Is(bootstrapErr.err, errDeliveryNotConfirmed)
		}
		err = bootstrapErr.err
	}
	var statusErr client.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Status == -328
	}
	if errors.Is(err, client.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
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
		kc.cleanupPumpDone = nil
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
		return nil, bootstrapFailure{stage: "connect", err: err}
	}
	if err := kc.resumeGroupCreates(ctx, c); err != nil {
		return nil, bootstrapFailure{stage: "group-create-recovery", err: err}
	}
	if err := kc.discoverGroups(ctx, c); err != nil {
		return nil, bootstrapFailure{stage: "group-discovery", err: err}
	}
	if err := kc.catchUp(ctx, c); err != nil {
		return nil, bootstrapFailure{stage: "catch-up", err: err}
	}
	if err := kc.refreshAnnouncements(ctx); err != nil {
		return nil, bootstrapFailure{stage: "announcements", err: err}
	}
	stream, err := c.Events(ctx)
	if err != nil {
		return nil, bootstrapFailure{stage: "events", err: err}
	}
	return stream, nil
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
			if result := kc.queue(kc.gapNotice(target.ChatID, target.MaxLogID)); !committable(result) {
				return fmt.Errorf("catch-up gap notice: %w", errDeliveryNotConfirmed)
			}
			continue
		} else if err != nil {
			if isUnidentifiableMessageError(err) {
				return unidentifiableMessageError()
			}
			return fmt.Errorf("catch up chat: %w", err)
		}
		for _, evt := range missed {
			if !kc.handleEvent(c, evt) {
				return fmt.Errorf("catch-up event: %w", errDeliveryNotConfirmed)
			}
		}
	}
	return nil
}

// run consumes the typed event stream until the session ends. It is the only
// goroutine that queues remote events or commits, which preserves the
// client's per-chat commit order.
func (kc *KakaoClient) run(c kakaoClient, stream <-chan events.Result, done chan struct{}, generation uint64) {
	kickedOut := false
	changeServer := false
	deliveryFailed := false
	var terminalErr error
	profileTicker := time.NewTicker(time.Minute)
	defer profileTicker.Stop()
eventLoop:
	for {
		var result events.Result
		select {
		case <-profileTicker.C:
			if !kickedOut && !changeServer {
				kc.refreshOneGroupProfiles(c)
			}
			continue
		case next, ok := <-stream:
			if !ok {
				break eventLoop
			}
			result = next
		}
		if kickedOut || changeServer {
			// A terminal notice ends the session's event acceptance window. The
			// stream still has to close so the owner can publish its terminal
			// bridge state, but later packets must not be committed.
			continue
		}
		if result.Err != nil {
			if errors.Is(result.Err, events.ErrUnidentifiableMembership) {
				terminalErr = events.ErrUnidentifiableMembership
				break eventLoop
			}
			if isUnidentifiableMessageError(result.Err) {
				terminalErr = unidentifiableMessageError()
				break eventLoop
			}
			kc.log().Warn().Err(result.Err).Msg("Dropped undecodable Kakao event")
			continue
		}
		if _, ok := result.Event.(events.Kickout); ok {
			kickedOut = true
		}
		if _, ok := result.Event.(events.ChangeServer); ok {
			changeServer = true
		}
		if !kc.handleEvent(c, result.Event) && !kickedOut && !changeServer {
			// Continuing would strand the first uncommitted event while later
			// deliveries could reach Matrix out of order. Release this session
			// and let bounded connector recovery replay from durable progress.
			// Outbound mutations are never replayed by this path.
			deliveryFailed = true
			break eventLoop
		}
	}
	kc.mu.Lock()
	stopping := kc.stopping
	kc.mu.Unlock()
	switch {
	case stopping:
	case terminalErr != nil:
		classification := stateUnidentifiableMsg
		if errors.Is(terminalErr, events.ErrUnidentifiableMembership) {
			classification = stateGroupMembershipInvalid
		}
		kc.log().Error().Str("classification", string(classification)).Msg("Kakao event admission stopped; operator recovery is required")
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: classification})
	case kickedOut:
		kc.sendState(status.BridgeState{StateEvent: status.StateBadCredentials, Error: stateKickedOut})
	case changeServer:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateChangeServer})
	case deliveryFailed:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDeliveryPaused})
	default:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDisconnected})
	}
	kc.sessionEnded(c, done, generation, kickedOut, changeServer, terminalErr)
}

func (kc *KakaoClient) sessionEnded(c kakaoClient, done chan struct{}, generation uint64, kickedOut, changeServer bool, terminalErr error) {
	close(done)
	kc.mu.Lock()
	if kc.generation != generation || kc.client != c {
		kc.mu.Unlock()
		return
	}
	stopping := kc.stopping
	kc.client = nil
	kc.done = nil
	if !stopping {
		kc.cleanup = c
		kc.cleanupPumpDone = nil
		kc.cleanupDone = make(chan struct{})
		kc.cleanupBusy = true
	}
	kc.mu.Unlock()
	if stopping {
		return
	}
	shutdownErr := shutdownWithRecoveryTimeout(c)
	kc.mu.Lock()
	if kc.cleanup == c {
		kc.cleanupBusy = false
		if kc.cleanupDone != nil {
			close(kc.cleanupDone)
			kc.cleanupDone = nil
		}
		if shutdownErr == nil {
			kc.cleanup = nil
			kc.cleanupPumpDone = nil
		}
	}
	canRecover := shutdownErr == nil && !kc.stopping && kc.generation == generation
	kc.mu.Unlock()
	if shutdownErr != nil {
		kc.log().Err(shutdownErr).Msg("Failed to close Kakao client before recovery")
		kc.scheduleCleanupRetry(c, generation, kickedOut, terminalErr == nil)
		return
	}
	if kickedOut || terminalErr != nil {
		return
	}
	if canRecover {
		kc.retryAfter(client.ErrClosed, generation)
	}
}

// scheduleCleanupRetry keeps a timed-out profile owner attached and permits
// one later cleanup attempt. No replacement can be opened while this owner is
// retained; this is deliberately separate from the recovery attempt budget.
func (kc *KakaoClient) scheduleCleanupRetry(c kakaoClient, generation uint64, kickedOut, recover bool) {
	kc.mu.Lock()
	if kc.cleanup != c || kc.stopping || kc.cleanupRetryCancel != nil || kc.cleanupRetryAttempts >= 1 {
		kc.mu.Unlock()
		return
	}
	kc.cleanupRetryAttempts++
	ctx, cancel := context.WithCancel(context.Background())
	kc.cleanupRetryCancel = cancel
	kc.cleanupRetryDone = make(chan struct{})
	cleanupRetryDone := kc.cleanupRetryDone
	kc.cleanupRetryID++
	retryID := kc.cleanupRetryID
	wait := kc.wait
	kc.mu.Unlock()
	go func() {
		defer func() {
			close(cleanupRetryDone)
			kc.mu.Lock()
			if kc.cleanupRetryID == retryID {
				kc.cleanupRetryCancel = nil
				kc.cleanupRetryDone = nil
			}
			kc.mu.Unlock()
		}()
		if err := wait(ctx, time.Second); err != nil {
			return
		}
		kc.mu.Lock()
		if kc.cleanup != c || kc.stopping || kc.cleanupRetryID != retryID {
			kc.mu.Unlock()
			return
		}
		kc.cleanupBusy = true
		if kc.cleanupDone == nil {
			kc.cleanupDone = make(chan struct{})
		}
		kc.mu.Unlock()
		err := shutdownWithRecoveryTimeout(c)
		kc.mu.Lock()
		if kc.cleanup == c {
			kc.cleanupBusy = false
			if kc.cleanupDone != nil {
				close(kc.cleanupDone)
				kc.cleanupDone = nil
			}
			if err == nil {
				kc.cleanup = nil
				kc.cleanupPumpDone = nil
			}
		}
		canRecover := err == nil && !kc.stopping && kc.generation == generation
		kc.mu.Unlock()
		if canRecover && !kickedOut && recover {
			kc.retryAfter(client.ErrClosed, generation)
		}
	}()
}

func shutdownWithRecoveryTimeout(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryCleanupTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}

func (kc *KakaoClient) handleEvent(c kakaoClient, evt events.Event) bool {
	kc.groupGate.Lock()
	defer kc.groupGate.Unlock()
	var removedChat int64
	switch notice := evt.(type) {
	case events.ChatLeft:
		removedChat = notice.ChatID
	case events.MemberRemoved:
		if notice.UserID == kc.userID {
			removedChat = notice.ChatID
		}
	}
	if removedChat > 0 {
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupAccessRemoved})
		if err := kc.recordSourceRemoval(context.Background(), removedChat); err != nil {
			return false
		}
		// The departed account cannot authorize a metadata request. Apply only
		// its explicit leave rather than fetching CHATINFO after removal.
		evt = events.ChatLeft{ChatID: removedChat}
	}
	if err := kc.checkUnresolvedGroupCreates(); err != nil {
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupCreateUnresolved})
		return false
	}
	if removedChat == 0 {
		switch notice := evt.(type) {
		case events.MemberAdded:
			if handled, success := kc.managedMembershipEvent(context.Background(), c, notice.ChatID, true); handled {
				return success
			}
		case events.MemberRemoved:
			if handled, success := kc.managedMembershipEvent(context.Background(), c, notice.ChatID, false); handled {
				return success
			}
		}
	}
	if reaction, ok := evt.(events.ReactionChanged); ok {
		if kc.checkSourceAccess(context.Background(), reaction.ChatID, nil) != nil {
			return false
		}
		remote, err := kc.reactionRemote(context.Background(), c, reaction)
		if err != nil {
			return kc.reportReactionFailure(reaction, err)
		}
		if remote == nil {
			return true
		}
		remotes := []bridgev2.RemoteEvent{remote}
		if syncEvent, ok := remote.(*kakaoReactionSync); ok && kc.login != nil && kc.login.Bridge != nil && kc.login.Bridge.DB != nil {
			remotes, err = kc.reactionDeliveryEvents(context.Background(), syncEvent)
			if err != nil {
				if errors.Is(err, errReactionTargetMissing) {
					kc.log().Debug().Msg("Kakao reaction target is not bridged; leaving revision for replay")
					return false
				}
				kc.log().Warn().Err(err).Msg("Kakao reaction delivery plan could not be built")
				return false
			}
		}
		allIgnored := true
		for _, delivery := range remotes {
			result := kc.queue(delivery)
			if !committable(result) {
				kc.log().Warn().Err(result.Error).Msg("Kakao reaction update was not confirmed as bridged")
				return false
			}
			if !result.Ignored {
				allIgnored = false
			}
			if syncEvent, ok := delivery.(*simplevent.Reaction); ok && syncEvent.Type == bridgev2.RemoteEventReactionRemove && kc.login != nil && kc.login.Bridge != nil && kc.login.Bridge.DB != nil {
				row, queryErr := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(context.Background(), kc.login.ID, syncEvent.TargetMessage, syncEvent.Sender.Sender, syncEvent.EmojiID)
				if queryErr != nil || row != nil {
					kc.log().Warn().Err(queryErr).Msg("Kakao reaction removal was not confirmed in the database")
					return false
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
				return false
			}
		}
		return true
	}
	if notice, ok := evt.(events.ReadStateChanged); ok {
		if kc.checkSourceAccess(context.Background(), notice.ChatID, nil) != nil {
			return false
		}
		return kc.handleReadState(notice)
	}
	remote := kc.remoteEventFor(evt)
	if remote != nil && removedChat == 0 {
		chatID, parseErr := parseChatID(remote.GetPortalKey().ID)
		if parseErr != nil || kc.checkSourceAccess(context.Background(), chatID, nil) != nil {
			return false
		}
	}
	if added, ok := evt.(events.MemberAdded); ok {
		change := remote.(*chatInfoChangeEvent)
		if err := kc.prepareMemberDiscovery(context.Background(), c, added.ChatID, change); err != nil {
			kc.log().Warn().Msg("Kakao group discovery snapshot failed; leaving event uncommitted")
			return false
		}
	}
	if remote == nil {
		kc.log().Debug().Str("kind", string(evt.Kind())).Msg("Ignoring Kakao event not bridged yet")
		return true
	}
	var result bridgev2.EventHandlingResult
	if removedChat > 0 {
		err := kc.applySourceLeave(context.Background(), removedChat)
		result = bridgev2.EventHandlingResult{Success: err == nil, Error: err}
	} else {
		result = kc.queue(remote)
	}
	if !committable(result) {
		kc.log().Warn().Err(result.Error).
			Bool("success", result.Success).
			Bool("queued", result.Queued).
			Msg("Kakao message was not confirmed as bridged; leaving it uncommitted for replay")
		return false
	}
	if result.Ignored {
		if message, ok := remote.(bridgev2.RemoteMessage); ok && !kc.ignoredMessageHasMapping(context.Background(), message) {
			kc.log().Warn().Msg("Ignored Kakao message has no confirmed mapping; retaining source progress")
			return false
		}
	}
	if removedChat > 0 && kc.verifySourceLeave(context.Background(), removedChat) != nil {
		return false
	}
	// Membership and metadata events can be delivered to the bridge without
	// carrying a Kakao message cursor. They are complete once the bridge
	// accepts them; attempting CommitEvent would report a successful refresh as
	// an ErrProtocol because no message position exists.
	if _, _, ok := events.MessagePosition(evt); !ok {
		return true
	}
	if err := c.CommitEvent(evt); err != nil {
		kc.log().Err(err).Msg("Failed to commit bridged Kakao message")
		return false
	}
	return true
}

// The SDK suppresses some Matrix refusals into Ignored success. Only a
// persisted message mapping can authorize source progress for such a result.
func (kc *KakaoClient) ignoredMessageHasMapping(ctx context.Context, message bridgev2.RemoteMessage) bool {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return false
	}
	parts, err := kc.login.Bridge.DB.Message.GetAllPartsByID(ctx, kc.login.ID, message.GetID())
	if err != nil || len(parts) == 0 {
		return false
	}
	if multipart, ok := message.(interface{ ExpectedPartIDs() []networkid.PartID }); ok {
		mapped := make(map[networkid.PartID]bool, len(parts))
		for _, part := range parts {
			mapped[part.PartID] = true
		}
		expected := multipart.ExpectedPartIDs()
		if len(expected) == 0 {
			return false
		}
		for _, partID := range expected {
			if !mapped[partID] {
				return false
			}
		}
	}
	return true
}

// committable reports whether the bridge finished handling an event. Ignored
// events count as handled at this boundary; source messages additionally need
// persisted mapping verification in handleEvent. A queued result,
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
	if kc.lifecycleCancel != nil {
		kc.lifecycleCancel()
		kc.lifecycle, kc.lifecycleCancel = nil, nil
	}
	var retryDone chan struct{}
	if kc.retryCancel != nil {
		kc.retryCancel()
		kc.retryCancel = nil
		retryDone = kc.retryDone
		kc.retryDone = nil
	}
	kc.retryID++
	if kc.cleanupRetryCancel != nil {
		kc.cleanupRetryCancel()
		kc.cleanupRetryCancel = nil
	}
	cleanupRetryDone := kc.cleanupRetryDone
	kc.cleanupRetryDone = nil
	kc.cleanupRetryID++
	if kc.connectCancel != nil {
		kc.connectCancel()
	}
	kc.generation++
	c := kc.client
	done := kc.done
	if c != nil && done != nil {
		kc.cleanupPumpDone = done
	}
	if c == nil {
		c = kc.cleanup
		// A retained owner keeps the original event-pump completion signal.
		// Retry cleanup must join that same worker before releasing ownership.
		done = kc.cleanupPumpDone
	}
	ownedByOther := c != nil && kc.cleanupBusy
	kc.stopping = true
	kc.client = nil
	kc.done = nil
	if c != nil && !kc.cleanupBusy {
		kc.cleanupBusy = true
		if kc.cleanupDone == nil {
			kc.cleanupDone = make(chan struct{})
		}
	}
	if c != nil {
		kc.cleanup = c
	}
	kc.mu.Unlock()
	joinWorkers := func() bool {
		for _, worker := range []chan struct{}{retryDone, cleanupRetryDone} {
			if worker == nil {
				continue
			}
			select {
			case <-worker:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}
	if c == nil {
		joinWorkers()
		return
	}
	// A recovery cleanup already owns Shutdown. Wait for it, then release the
	// lease without opening a replacement behind its back.
	kc.mu.Lock()
	busy := ownedByOther && kc.cleanupBusy && kc.cleanup == c
	cleanupDone := kc.cleanupDone
	kc.mu.Unlock()
	if busy && cleanupDone != nil {
		select {
		case <-cleanupDone:
		case <-ctx.Done():
			return
		}
		joinWorkers()
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
		kc.mu.Lock()
		if kc.cleanup == c {
			kc.cleanupBusy = false
			if kc.cleanupDone != nil {
				close(kc.cleanupDone)
				kc.cleanupDone = nil
			}
		}
		kc.mu.Unlock()
		joinWorkers()
		return
	}
	kc.mu.Lock()
	if kc.cleanup == c {
		kc.cleanupBusy = false
		if kc.cleanupDone != nil {
			close(kc.cleanupDone)
		}
		kc.cleanup = nil
		kc.cleanupPumpDone = nil
		kc.cleanupDone = nil
	}
	kc.mu.Unlock()
	joinWorkers()
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
	return kc.getChatInfo(ctx, portal, false)
}

func (kc *KakaoClient) getChatInfo(ctx context.Context, portal *bridgev2.Portal, regularGroupOnly bool) (*bridgev2.ChatInfo, error) {
	if portal == nil {
		return nil, errChatInfoMismatch
	}
	c, err := kc.metadataClient()
	if err != nil {
		return nil, err
	}
	return kc.chatInfoFromClient(ctx, portal, c, regularGroupOnly)
}

func (kc *KakaoClient) chatInfoFromClient(ctx context.Context, portal *bridgev2.Portal, c kakaoClient, regularGroupOnly bool, requirePersonal ...bool) (*bridgev2.ChatInfo, error) {
	chatID, err := parseChatID(portal.ID)
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
	if regularGroupOnly && data.Type != "MultiChat" {
		return nil, errors.New("connector: discovery requires a regular group")
	}
	if data.LinkID > 0 {
		return nil, errUnsupportedOpenChatMetadata
	}
	if data.Type == "MultiChat" {
		if data.Meta == nil {
			data.Meta, err = c.PersonalMeta(ctx, chatID)
			if err != nil {
				return nil, err
			}
		}
		if len(requirePersonal) > 0 && requirePersonal[0] && data.Meta == nil {
			return nil, errors.New("connector: personal room metadata unavailable; refresh did not confirm a clear")
		}
		data, err = kc.checkpointGroupDisplay(ctx, portal, data)
		if err != nil {
			return nil, err
		}
	}

	// MEMLIST is a UI-originated API in the official client and its stored
	// room token is not exposed by ChatData. An initial bridge sync therefore
	// starts at token zero; later membership updates remain separate work.
	roster, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
		return nil, err
	}
	completeRoster := len(roster.MemberIDs) > 0
	if regularGroupOnly && !completeRoster {
		return nil, errInvalidMemberRoster
	}
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
		if data.Type != "MultiChat" {
			return nil, err
		}
		// The successfully refreshed source roster remains authoritative even
		// when an optional profile batch fails. Keep successful earlier results
		// and leave unresolved identities without fabricated profile fields.
		kc.log().Warn().Int("resolved_profiles", len(profiles)).Int("requested_profiles", len(userIDs)).
			Msg("Some member profiles are unavailable; preserving the source roster")
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
	if data.Type == "MultiChat" {
		display, displayErr := chatmeta.ProjectGroupDisplay(data)
		if displayErr != nil {
			return nil, displayErr
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
				info.Avatar = avatarFromURL(avatarURL)
			}
		}
	} else {
		if name := chatName(data); name != "" {
			info.Name = &name
		}
		if data.Meta != nil {
			info.Avatar = avatarFromURL(data.Meta.ImageURL)
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
		announcement, err := kc.announcementInfo(ctx, portal, c)
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
	// Owned three-person group observation: CHATINFO carries the shared
	// room name as Title shared metadata even when the personal m is absent.
	// A newer empty value clears the name instead of reviving an older one.
	var sharedName *chatmeta.ChatMeta
	for i := range data.ChatMetas {
		meta := &data.ChatMetas[i]
		if meta.Type == chatmeta.SharedMetaTitle && (sharedName == nil || meta.Revision > sharedName.Revision) {
			sharedName = meta
		}
	}
	if sharedName != nil && sharedName.Content != "" {
		return sharedName.Content
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
	// This function consumes a known MEMBER record. Empty fields confirm a clear;
	// an unavailable profile never calls it and carries no UserInfo instead.
	name := profile.Nickname
	info := &bridgev2.UserInfo{Name: &name}
	raw := profile.ProfileImageURL
	if raw == "" {
		raw = profile.FullProfileImageURL
	}
	if raw == "" {
		info.Avatar = &bridgev2.Avatar{Remove: true}
	} else {
		info.Avatar = avatarFromURL(raw)
	}
	return info
}

func (kc *KakaoClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID:               "com.github.frrad.mooo.capabilities.2026_10_05.photos1.reactions1.receipts1",
		MaxTextLength:    maxTextLength,
		Reply:            event.CapLevelPartialSupport,
		Reaction:         event.CapLevelPartialSupport,
		ReactionCount:    1,
		ReadReceipts:     true,
		AllowedReactions: []string{"❤️", "👍", "✅", "😆", "😮", "😢"},
		File: event.FileFeatureMap{event.MsgImage: &event.FileFeatures{
			MimeTypes: map[string]event.CapabilitySupportLevel{"image/jpeg": event.CapLevelPartialSupport, "image/png": event.CapLevelPartialSupport},
			MaxSize:   media.MaxImageBytes,
		}},
	}
}

// maxTextLength is a conservative bound; the official limit is not yet
// established.
const maxTextLength = 10000

const matrixImageTransferTimeout = 30 * time.Second

var matrixImageDownloader = downloadMatrixImageBounded

// HandleMatrixMessage sends one plain text message. A failed or ambiguous
// send is reported to Matrix and never retried.
func (kc *KakaoClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	// No connector gate here: the SDK calls this while holding the portal's
	// event lock, and the Kakao pump holds the gate while queueing into that
	// portal. Source blocks are set in memory before they are persisted, so
	// checkSourceAccess remains the authority.
	if msg == nil || msg.Portal == nil || msg.Content == nil {
		return nil, errSourceAccessRemoved
	}
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote, event.MsgImage:
	default:
		return nil, bridgev2.ErrUnsupportedMessageType
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return nil, err
	}
	if err = kc.checkSourceAccess(ctx, chatID, msg.Portal); err != nil {
		return nil, err
	}
	if msg.ReplyTo == nil && msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplyTo() != "" {
		return nil, replyTargetStatus(errMissingReplyMetadata)
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	if msg.Content.MsgType == event.MsgImage {
		if msg.ReplyTo != nil || msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplyTo() != "" {
			return nil, errUnsupportedImageReply
		}
		if msg.Portal == nil || msg.Portal.Bridge == nil {
			return nil, bridgev2.ErrFailedToGetIntent
		}
		if msg.Content.Info != nil && msg.Content.Info.Size > media.MaxImageBytes {
			return nil, media.ErrInvalidImage
		}
		intent, ok := msg.Portal.GetIntentFor(ctx, kc.selfSender(), kc.login, bridgev2.RemoteEventMessage)
		if !ok {
			return nil, bridgev2.ErrFailedToGetIntent
		}
		return kc.sendMatrixImage(ctx, c, intent, chatID, msg.Content, func(ctx context.Context) error {
			return kc.beginOutbound(ctx, msg)
		})
	}
	body := msg.Content.Body
	if msg.Content.MsgType == event.MsgEmote {
		body = "* " + body
	}
	var response chat.WriteResponse
	if msg.ReplyTo != nil {
		target, err := replyTargetFor(msg.ReplyTo, msg.Portal.PortalKey)
		if err != nil {
			return nil, replyTargetStatus(err)
		}
		if err := kc.beginOutbound(ctx, msg); err != nil {
			return nil, err
		}
		response, err = c.SendReply(ctx, chat.ReplyRequest{ChatID: chatID, Message: body, Target: target})
		if err != nil {
			return nil, outboundSendError(err)
		}
	} else {
		if err := kc.beginOutbound(ctx, msg); err != nil {
			return nil, err
		}
		response, err = c.SendText(ctx, chatID, body)
		if err != nil {
			return nil, outboundSendError(err)
		}
	}
	if response.LogID <= 0 {
		return nil, outboundAcceptedWithoutPosition(errors.New("KakaoTalk accepted the message without a log ID"))
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

// replyTargetStatus refuses a Matrix reply whose target cannot be mapped to
// exactly one KakaoTalk message in this chat. Nothing was sent, so the
// refusal is certain; sending without the relation is left to the user.
func replyTargetStatus(err error) error {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithMessage("The message you replied to is not a bridged KakaoTalk message in this chat, so the reply was not sent. Send it without the reply to deliver it.").
		WithSendNotice(true)
}

// outboundSendError classifies a failed single Kakao send for Matrix. A
// server status reply is a certain refusal. Any other failure may have been
// delivered; it is never retried, and it must not be shown as retriable,
// because a manual resend could duplicate a delivered message.
func outboundSendError(err error) error {
	var statusErr client.StatusError
	if errors.As(err, &statusErr) {
		return bridgev2.WrapErrorInStatus(err).
			WithStatus(event.MessageStatusFail).
			WithErrorReason(event.MessageStatusNetworkError).
			WithIsCertain(true).
			WithMessage("KakaoTalk refused this message.").
			WithSendNotice(true)
	}
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusNetworkError).
		WithIsCertain(false).
		WithMessage("KakaoTalk did not confirm this message, and it may have been delivered. Check KakaoTalk before sending it again.").
		WithSendNotice(true)
}

func outboundAcceptedWithoutPosition(err error) error {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusNetworkError).
		WithIsCertain(false).
		WithMessage("KakaoTalk accepted this message without identifying it, so it is probably delivered but cannot be linked. Check KakaoTalk before sending it again.").
		WithSendNotice(true)
}

// sendMatrixImage fetches and validates the Matrix image before begin
// reserves the event's single source send, so a failed fetch or an image
// KakaoTalk cannot accept never consumes the reservation or reaches Kakao.
// Disconnect interrupts the transfer through the connection lifecycle.
func (kc *KakaoClient) sendMatrixImage(ctx context.Context, c kakaoClient, intent bridgev2.MatrixAPI, chatID int64, content *event.MessageEventContent, begin func(context.Context) error) (*bridgev2.MatrixMessageResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	if lifecycle := kc.connectionLifecycle(); lifecycle != nil {
		var release func()
		ctx, release = withConnectionLifecycle(context.WithValue(ctx, connectionLifecycleKey{}, lifecycle))
		defer release()
	}
	// A body that differs from the filename is the image's caption.
	caption := ""
	if content.FileName != "" && content.Body != content.FileName {
		caption = content.Body
	}
	if !media.ValidCaption(caption) {
		return nil, bridgev2.WrapErrorInStatus(media.ErrInvalidCaption).
			WithStatus(event.MessageStatusFail).
			WithErrorReason(event.MessageStatusUnsupported).
			WithIsCertain(true).
			WithMessage("The image caption is too long for KakaoTalk; the image was not sent.").
			WithSendNotice(true)
	}
	data, err := matrixImageDownloader(ctx, intent, content.URL, content.File)
	if err != nil {
		return nil, bridgev2.WrapErrorInStatus(errors.New("download Matrix image failed")).
			WithStatus(event.MessageStatusRetriable).
			WithErrorReason(event.MessageStatusNetworkError).
			WithIsCertain(true).
			WithMessage("The image could not be fetched from Matrix, so it was not sent to KakaoTalk.").
			WithSendNotice(true)
	}
	if _, err := media.PrepareImage(data); err != nil {
		return nil, bridgev2.WrapErrorInStatus(err).
			WithStatus(event.MessageStatusFail).
			WithErrorReason(event.MessageStatusUnsupported).
			WithIsCertain(true).
			WithMessage("KakaoTalk accepts only JPEG and PNG photos up to 16 MiB; this image was not sent.").
			WithSendNotice(true)
	}
	if err := begin(ctx); err != nil {
		return nil, err
	}
	response, err := c.SendImage(ctx, chatID, data, caption)
	if err != nil {
		return nil, outboundSendError(err)
	}
	logID, sendAt, err := media.SendResultPosition(response)
	if err != nil {
		return nil, outboundAcceptedWithoutPosition(err)
	}
	return &bridgev2.MatrixMessageResponse{DB: &database.Message{ID: makeMessageID(chatID, logID), SenderID: makeUserID(kc.userID), Timestamp: kakaoTime(sendAt), Metadata: newKakaoMessageMetadata(chatID, logID, kc.userID, media.PhotoType, "[image]", 0)}}, nil
}

func downloadMatrixImageBounded(ctx context.Context, intent bridgev2.MatrixAPI, uri id.ContentURIString, fileInfo *event.EncryptedFileInfo) ([]byte, error) {
	asIntent, ok := intent.(*bridgematrix.ASIntent)
	if !ok || asIntent == nil || asIntent.Matrix == nil {
		return nil, bridgev2.ErrFailedToGetIntent
	}
	if fileInfo != nil {
		uri = fileInfo.URL
		if err := fileInfo.PrepareForDecryption(); err != nil {
			return nil, media.ErrInvalidImage
		}
	}
	parsed, err := uri.Parse()
	if err != nil {
		return nil, media.ErrInvalidImage
	}
	resp, err := asIntent.Matrix.Download(ctx, parsed)
	if err != nil {
		return nil, media.ErrInvalidImage
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > media.MaxImageBytes {
		return nil, media.ErrInvalidImage
	}
	reader := io.Reader(resp.Body)
	var closeReader io.Closer = resp.Body
	if fileInfo != nil {
		decryptReader := fileInfo.DecryptStream(resp.Body)
		reader = decryptReader
		closeReader = decryptReader
	}
	return readBoundedMatrixImage(reader, closeReader)
}

func readBoundedMatrixImage(reader io.Reader, closer io.Closer) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(reader, media.MaxImageBytes+1))
	closeErr := closer.Close()
	if readErr != nil || closeErr != nil || len(data) > media.MaxImageBytes {
		return nil, media.ErrInvalidImage
	}
	return data, nil
}

func (kc *KakaoClient) selfSender() bridgev2.EventSender {
	return bridgev2.EventSender{
		IsFromMe:    true,
		SenderLogin: kc.login.ID,
		Sender:      makeUserID(kc.userID),
	}
}
