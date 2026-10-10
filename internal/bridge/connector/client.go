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
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"github.com/frrad/mooo/internal/protocol/reactions"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// The connector depends on client.Client through these role interfaces so
// the event loop and send paths can be tested without a Kakao backend. Every
// method is required: a missing one is a compile error, never a silently
// skipped feature.

// sessionAPI owns the connection and the live event stream.
type sessionAPI interface {
	Connect(ctx context.Context) error
	Events(ctx context.Context) (<-chan events.Result, error)
	CommitEvent(event events.Event) error
	ResumeTargets(ctx context.Context) ([]syncmsg.Target, error)
	CatchUp(ctx context.Context, chatID, targetMax int64) ([]events.Event, error)
	Shutdown(ctx context.Context) error
}

// chatMetaAPI reads chat inventory, metadata and rosters.
type chatMetaAPI interface {
	ListChats(ctx context.Context) ([]chatmeta.ChatData, error)
	ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error)
	PersonalMeta(ctx context.Context, chatID int64) (*chatmeta.RoomMeta, error)
	MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error)
	Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error)
	MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error)
	ChatOnRoom(ctx context.Context, chatID int64) (chatmeta.ChatOnRoomResponse, error)
}

// outboundAPI sends and mutates messages and chats on Kakao.
type outboundAPI interface {
	SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error)
	SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error)
	SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error)
	SendUpload(ctx context.Context, chatID int64, upload media.Upload) (media.SendResult, error)
	SendAlbum(ctx context.Context, chatID int64, photos [][]byte, caption string) (chat.WriteResponse, error)
	ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error)
	DeleteMessage(ctx context.Context, request chat.DeleteRequest) error
	MarkRead(ctx context.Context, chatID, watermark int64) (syncmsg.Response, error)
	CreateChat(context.Context, chat.CreateRequest) (chat.CreateResponse, error)
	AddMembers(context.Context, chat.AddMembersRequest) (chat.AddMembersResponse, error)
}

// reactionAPI mutates and reads message reactions.
type reactionAPI interface {
	React(context.Context, reactions.Request) (reactions.Response, error)
	ReactionMembers(ctx context.Context, chatID, logID int64) (reactions.MembersResponse, error)
	MiniReactionDetails(ctx context.Context, chatID, linkID, logID int64) (reactions.DetailsResponse, error)
	ReactionMetaSync(ctx context.Context, chatID, cur int64) (reactions.SyncMetaPage, error)
}

// historyAPI reads message history by range or position.
type historyAPI interface {
	ReadHistoryPage(ctx context.Context, chatID, after, through int64, limit int) (client.HistoryPage, error)
	GetMessages(ctx context.Context, chatID int64, logIDs []int64) ([]events.Event, error)
	InitialSyncTargets(ctx context.Context) ([]syncmsg.Target, error)
}

// kakaoClient is everything the connector uses from client.Client.
type kakaoClient interface {
	sessionAPI
	chatMetaAPI
	outboundAPI
	reactionAPI
	historyAPI
}

var _ kakaoClient = (*client.Client)(nil)

type bootstrapFailure struct {
	stage string
	err   error
}

func (e bootstrapFailure) Error() string { return e.stage + ": " + e.err.Error() }
func (e bootstrapFailure) Unwrap() error { return e.err }

func openProfileClient(statePath string, packetObserver func(loco.Packet)) (kakaoClient, error) {
	return client.OpenWithOptions(statePath, nil, client.OpenOptions{FullChatList: true, PacketObserver: packetObserver})
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
	store  store
	userID int64
	open   func() (kakaoClient, error)

	// queue and sendState default to the login's bridge methods and are
	// replaced in tests.
	queue     func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult
	sendState func(status.BridgeState)
	// reserveOutbound defaults to the bridge's durable store.
	reserveOutbound func(context.Context, id.EventID) (bool, error)
	// refreshMembership re-reads a group's source roster outside the portal
	// event loop that delivered a Matrix change.
	refreshMembership func(kakaoClient, int64)

	groupGate   sync.Mutex
	displayGate sync.Mutex

	// cacheMu guards the source caches below. It is never held together with
	// mu, and never across I/O.
	cacheMu       sync.Mutex
	sourceBlocked map[int64]error
	profiles      map[int64]chatmeta.Member
	// profileRefreshAfter is the periodic profile refresh cursor; groupGate
	// guards it.
	profileRefreshAfter string

	reactionNoticeMu sync.Mutex
	reactionNotices  map[string]time.Time

	// mu guards the connection lifecycle state below.
	mu             sync.Mutex
	disconnectGate chan struct{}
	client         kakaoClient
	cleanup        kakaoClient
	connecting     bool
	stopping       bool
	done           chan struct{}
	retained       ownerCleanup
	// cleanupRetry is the single delayed retry of a timed-out owner cleanup;
	// cleanupRetryAttempts bounds it per connected session.
	cleanupRetry         worker
	cleanupRetryAttempts int
	connectCancel        context.CancelFunc
	// lifecycle spans one Connect..Disconnect cycle, including bounded
	// recovery reconnects. Disconnect cancels it before waiting for the event
	// pump, which interrupts in-flight inbound conversions.
	lifecycle       context.Context
	lifecycleCancel context.CancelFunc
	// retry waits out a recovery backoff and then reconnects.
	retry                worker
	generation           uint64
	connectingGeneration uint64
	recoveryTry          int
	wait                 func(context.Context, time.Duration) error
}

var (
	_ bridgev2.NetworkAPI           = (*KakaoClient)(nil)
	_ bridgev2.NetworkAPIWithUserID = (*KakaoClient)(nil)
)

func newKakaoClient(login *bridgev2.UserLogin, st store, userID int64, open func() (kakaoClient, error)) *KakaoClient {
	kc := &KakaoClient{
		login:           login,
		store:           st,
		userID:          userID,
		open:            open,
		queue:           login.QueueRemoteEvent,
		profiles:        make(map[int64]chatmeta.Member),
		reactionNotices: make(map[string]time.Time),
	}
	kc.wait = waitForRecovery
	kc.reserveOutbound = kc.reserveOutboundKV
	kc.refreshMembership = kc.refreshMembershipAsync
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
			kc.retain(c)
		}
		kc.mu.Unlock()
		kc.shutdownBootstrap(c, "stale bootstrap", true)
		return
	}
	// Retain the bootstrap owner before any potentially blocking Connect,
	// catch-up, or Events call. Disconnect must be able to interrupt and join a
	// client whose subscription has not returned yet.
	kc.mu.Lock()
	kc.retain(c)
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
	defer kc.mu.Unlock()
	if kc.stopping || kc.generation != generation || kc.recoveryTry >= maxRecoveryAttempts {
		return false
	}
	attempt := kc.recoveryTry
	kc.recoveryTry++
	wait := kc.wait
	delay := recoveryDelay(err, attempt)
	kc.retry.start(&kc.mu, func(ctx context.Context, retryID uint64) {
		if err := wait(ctx, delay); err != nil {
			return
		}
		kc.mu.Lock()
		if kc.stopping || kc.generation != generation || kc.client != nil || kc.cleanup != nil || kc.retry.id != retryID {
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
	})
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
	kc.release(c, nil)
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
	kc.resyncReactions(ctx, c)
	kc.recoverReadWatermarks(ctx, c)
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
		kc.retain(c)
		kc.retained.begin()
	}
	kc.mu.Unlock()
	if stopping {
		return
	}
	shutdownErr := shutdownWithRecoveryTimeout(c)
	kc.mu.Lock()
	kc.release(c, shutdownErr)
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
	defer kc.mu.Unlock()
	if kc.cleanup != c || kc.stopping || kc.cleanupRetry.running() || kc.cleanupRetryAttempts >= 1 {
		return
	}
	kc.cleanupRetryAttempts++
	wait := kc.wait
	kc.cleanupRetry.start(&kc.mu, func(ctx context.Context, retryID uint64) {
		if err := wait(ctx, time.Second); err != nil {
			return
		}
		kc.mu.Lock()
		if kc.cleanup != c || kc.stopping || kc.cleanupRetry.id != retryID {
			kc.mu.Unlock()
			return
		}
		kc.retained.begin()
		kc.mu.Unlock()
		err := shutdownWithRecoveryTimeout(c)
		kc.mu.Lock()
		kc.release(c, err)
		canRecover := err == nil && !kc.stopping && kc.generation == generation
		kc.mu.Unlock()
		if canRecover && !kickedOut && recover {
			kc.retryAfter(client.ErrClosed, generation)
		}
	})
}

func shutdownWithRecoveryTimeout(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryCleanupTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}

func (kc *KakaoClient) handleEvent(c kakaoClient, evt events.Event) bool {
	kc.groupGate.Lock()
	defer kc.groupGate.Unlock()
	ctx := context.Background()
	removedChat := kc.removedChatOf(evt)
	if removedChat > 0 && !kc.beginSourceRemoval(ctx, removedChat) {
		return false
	}
	if err := kc.checkUnresolvedGroupCreates(); err != nil {
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupCreateUnresolved})
		return false
	}
	if removedChat > 0 {
		return kc.applyRemovedSource(ctx, removedChat)
	}
	if handled, success := kc.handleMembershipEvent(ctx, c, evt); handled {
		return success
	}
	remote := kc.remoteEventFor(evt)
	chatID, guarded, err := sourceChatOf(evt, remote)
	if err != nil || (guarded && kc.checkSourceAccess(ctx, chatID) != nil) {
		return false
	}
	switch evt := evt.(type) {
	case events.ReactionChanged:
		handled, err := kc.applyReactionChange(c, evt)
		if err != nil {
			return kc.reportReactionFailure(evt, err)
		}
		return handled
	case events.ReadStateChanged:
		return kc.handleReadState(evt)
	case events.MessageEdited:
		if evt.Modified == nil {
			completed, err := kc.completeEdit(ctx, c, evt)
			if err != nil {
				kc.log().Warn().Err(err).Msg("Kakao edited message could not be fetched; leaving the edit uncommitted")
				return false
			}
			return kc.deliverRemote(c, completed, kc.remoteEventFor(completed))
		}
	case events.DeletedMessage:
		if kc.deletedFromMatrix(makeMessageID(evt.ChatID, evt.LogID)) {
			return kc.commitIfPositioned(c, evt, "Failed to commit Kakao log deleted from Matrix")
		}
	case events.MessageDeleted:
		if kc.targetAlreadyDeleted(evt) {
			return kc.commitIfPositioned(c, evt, "Failed to commit Kakao delete feed")
		}
	case events.MemberAdded:
		if err := kc.prepareMemberDiscovery(ctx, c, evt.ChatID, remote.(*chatInfoChangeEvent)); err != nil {
			kc.log().Warn().Msg("Kakao group discovery snapshot failed; leaving event uncommitted")
			return false
		}
	}
	return kc.deliverRemote(c, evt, remote)
}

// removedChatOf reports the chat whose source access an event revokes: an
// explicit leave, or the connected account's own removal.
func (kc *KakaoClient) removedChatOf(evt events.Event) int64 {
	switch notice := evt.(type) {
	case events.ChatLeft:
		return notice.ChatID
	case events.MemberRemoved:
		if notice.UserID == kc.userID {
			return notice.ChatID
		}
	}
	return 0
}

// beginSourceRemoval reports the lost access and persists the source block
// before anything else about the event is handled.
func (kc *KakaoClient) beginSourceRemoval(ctx context.Context, chatID int64) bool {
	kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupAccessRemoved})
	return kc.recordSourceRemoval(ctx, chatID) == nil
}

// applyRemovedSource applies the account's departure from a chat. The
// departed account cannot authorize a metadata request, so only its explicit
// leave is applied rather than fetching CHATINFO after removal.
func (kc *KakaoClient) applyRemovedSource(ctx context.Context, chatID int64) bool {
	if err := kc.applySourceLeave(ctx, chatID); err != nil {
		kc.log().Warn().Err(err).Bool("success", false).Bool("queued", false).
			Msg("Kakao message was not confirmed as bridged; leaving it uncommitted for replay")
		return false
	}
	return kc.verifySourceLeave(ctx, chatID) == nil
}

// sourceChatOf names the chat whose source access gates an event. Reactions
// and read states are gated by their own chat; every other event is gated
// only when it becomes a bridge event, by that event's portal.
func sourceChatOf(evt events.Event, remote bridgev2.RemoteEvent) (int64, bool, error) {
	switch evt := evt.(type) {
	case events.ReactionChanged:
		return evt.ChatID, true, nil
	case events.ReadStateChanged:
		return evt.ChatID, true, nil
	}
	if remote == nil {
		return 0, false, nil
	}
	chatID, err := parseChatID(remote.GetPortalKey().ID)
	return chatID, err == nil, err
}

// deliverRemote queues a bridge event and commits the source event once the
// bridge confirms it.
func (kc *KakaoClient) deliverRemote(c kakaoClient, evt events.Event, remote bridgev2.RemoteEvent) bool {
	if remote == nil {
		kc.log().Debug().Str("kind", string(evt.Kind())).Msg("Ignoring Kakao event not bridged yet")
		return true
	}
	result := kc.queue(remote)
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
	return kc.commitIfPositioned(c, evt, "Failed to commit bridged Kakao message")
}

// commitIfPositioned commits a handled source event. Membership and metadata
// events can be delivered to the bridge without carrying a Kakao message
// cursor. They are complete once the bridge accepts them; attempting
// CommitEvent would report a successful refresh as an ErrProtocol because no
// message position exists.
func (kc *KakaoClient) commitIfPositioned(c kakaoClient, evt events.Event, failure string) bool {
	if _, _, ok := events.MessagePosition(evt); !ok {
		return true
	}
	if err := c.CommitEvent(evt); err != nil {
		kc.log().Err(err).Msg(failure)
		return false
	}
	return true
}

// The SDK suppresses some Matrix refusals into Ignored success. Only a
// persisted message mapping can authorize source progress for such a result.
func (kc *KakaoClient) ignoredMessageHasMapping(ctx context.Context, message bridgev2.RemoteMessage) bool {
	parts, err := kc.store.messageParts(ctx, kc.login.ID, message.GetID())
	if err != nil || len(parts) == 0 {
		return false
	}
	if multipart, ok := message.(expectedPartsMessage); ok {
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
	retryDone := kc.retry.stop()
	cleanupRetryDone := kc.cleanupRetry.stop()
	if kc.connectCancel != nil {
		kc.connectCancel()
	}
	kc.generation++
	c := kc.client
	done := kc.done
	if c != nil && done != nil {
		kc.retained.pumpDone = done
	}
	if c == nil {
		c = kc.cleanup
		// A retained owner keeps the original event-pump completion signal.
		// Retry cleanup must join that same worker before releasing ownership.
		done = kc.retained.pumpDone
	}
	ownedByOther := c != nil && kc.retained.busy
	kc.stopping = true
	kc.client = nil
	kc.done = nil
	if c != nil && !kc.retained.busy {
		kc.retained.begin()
	}
	if c != nil {
		kc.cleanup = c
	}
	kc.mu.Unlock()
	joinWorkers := func() { join(ctx, retryDone, cleanupRetryDone) }
	if c == nil {
		joinWorkers()
		return
	}
	// A recovery cleanup already owns Shutdown. Wait for it, then release the
	// lease without opening a replacement behind its back.
	kc.mu.Lock()
	busy := ownedByOther && kc.retained.busy && kc.cleanup == c
	cleanupDone := kc.retained.done
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
	}
	kc.mu.Lock()
	kc.release(c, err)
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
	info, _, err := kc.chatInfoFromClient(ctx, portal, c, chatInfoOptions{regularGroupOnly: regularGroupOnly})
	return info, err
}

// GetUserInfo returns a profile previously resolved in a room. MEMBER is
// chat-scoped, so an uncached ghost has no source-supported name to return.
func (kc *KakaoClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	userID, err := parseUserID(string(ghost.ID))
	if err != nil {
		return nil, err
	}
	kc.cacheMu.Lock()
	profile, ok := kc.profiles[userID]
	kc.cacheMu.Unlock()
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
		Edit:             event.CapLevelPartialSupport,
		EditMaxAge:       secondsPtr(kakaoEditWindow),
		Delete:           event.CapLevelPartialSupport,
		DeleteMaxAge:     secondsPtr(kakaoDeleteWindow),
		File: event.FileFeatureMap{
			event.MsgImage: &event.FileFeatures{
				MimeTypes: map[string]event.CapabilitySupportLevel{"image/jpeg": event.CapLevelPartialSupport, "image/png": event.CapLevelPartialSupport},
				MaxSize:   media.MaxImageBytes,
			},
			// Files and audio carry no caption in KakaoTalk; video does.
			event.MsgFile:  uploadFeatures(event.CapLevelRejected),
			event.MsgAudio: uploadFeatures(event.CapLevelRejected),
			event.MsgVideo: uploadFeatures(event.CapLevelFullySupported),
			event.MsgBeeperGallery: &event.FileFeatures{
				MimeTypes:        map[string]event.CapabilitySupportLevel{"image/jpeg": event.CapLevelPartialSupport, "image/png": event.CapLevelPartialSupport},
				Caption:          event.CapLevelPartialSupport,
				MaxCaptionLength: media.MaxCaptionBytes,
				MaxSize:          media.MaxImageBytes,
			},
		},
	}
}

func secondsPtr(d time.Duration) *jsontime.Seconds {
	seconds := jsontime.S(d)
	return &seconds
}

func uploadFeatures(caption event.CapabilitySupportLevel) *event.FileFeatures {
	features := &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{"*/*": event.CapLevelPartialSupport},
		Caption:   caption,
		MaxSize:   media.MaxUploadBytes,
	}
	if caption != event.CapLevelRejected {
		features.MaxCaptionLength = media.MaxCaptionBytes
	}
	return features
}

// maxTextLength is a conservative bound; the official limit is not yet
// established.
const maxTextLength = 10000

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
	case event.MsgText, event.MsgNotice, event.MsgEmote, event.MsgImage, event.MsgFile, event.MsgVideo, event.MsgAudio, event.MsgBeeperGallery:
	default:
		return nil, bridgev2.ErrUnsupportedMessageType
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return nil, err
	}
	if err = kc.checkSourceAccess(ctx, chatID); err != nil {
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
	switch msg.Content.MsgType {
	case event.MsgImage:
		return kc.withMediaSend(ctx, msg, c, chatID, imageReplyRefusal, oversizedImage(msg.Content), kc.sendMatrixImage)
	case event.MsgFile, event.MsgVideo, event.MsgAudio:
		return kc.withMediaSend(ctx, msg, c, chatID, uploadReplyRefusal, nil, kc.sendMatrixUpload)
	case event.MsgBeeperGallery:
		return kc.withMediaSend(ctx, msg, c, chatID, uploadReplyRefusal, nil, kc.sendMatrixAlbum)
	}
	return kc.sendMatrixText(ctx, c, chatID, msg)
}

// sendMatrixText sends one Matrix text, notice or emote as a KakaoTalk text
// message, or as a reply when the Matrix event replies to a bridged message.
func (kc *KakaoClient) sendMatrixText(ctx context.Context, c outboundAPI, chatID int64, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	body := msg.Content.Body
	if msg.Content.MsgType == event.MsgEmote {
		body = "* " + body
	}
	var response chat.WriteResponse
	sentType := messagetype.Text
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
		sentType = messagetype.Reply
	} else {
		if err := kc.beginOutbound(ctx, msg); err != nil {
			return nil, err
		}
		var err error
		response, err = c.SendText(ctx, chatID, body)
		if err != nil {
			return nil, outboundSendError(err)
		}
	}
	if response.LogID <= 0 {
		return nil, outboundAcceptedWithoutPosition(errors.New("KakaoTalk accepted the message without a log ID"))
	}
	return kc.sentMessage(chatID, response.LogID, response.SendAt, sentType, body), nil
}

// sentMessage records the user's own message at the position KakaoTalk
// assigned to it.
func (kc *KakaoClient) sentMessage(chatID, logID, sendAt int64, typ int32, preview string) *bridgev2.MatrixMessageResponse {
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        makeMessageID(chatID, logID),
			SenderID:  makeUserID(kc.userID),
			Timestamp: kakaoTime(sendAt),
			Metadata:  newKakaoMessageMetadata(chatID, logID, kc.userID, typ, preview, 0),
		},
	}
}

func (kc *KakaoClient) selfSender() bridgev2.EventSender {
	return bridgev2.EventSender{
		IsFromMe:    true,
		SenderLogin: kc.login.ID,
		Sender:      makeUserID(kc.userID),
	}
}
