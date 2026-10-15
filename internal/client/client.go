package client

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/friends"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"github.com/frrad/mooo/internal/protocol/reactions"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrClientClosed = errors.New("client: client closed")

var errCommitOrder = errors.New("client: event commit is not the next delivered message for its chat")

// Client owns authenticated state and one long-lived LOCO session within the
// process that holds the profile's global owner lease. Operations establish
// that session once and reuse it. Initial establishment may perform one reviewed
// credential renewal followed by one fresh login when LOGINLIST returns -950.
// Client never reconnects automatically after a session disconnect: a caller
// must make an explicit lifecycle decision so an ambiguous mutation is not
// repeated.
type Client struct {
	mu               sync.Mutex
	connectActive    bool
	connectDone      chan struct{}
	shutdownActive   bool
	shutdownDone     chan struct{}
	state            authstate.State
	store            *authstate.Store
	checkpoint       *continuity.Store
	http             macweb.Doer
	session          *session
	cleanupSession   *session
	closed           bool
	renewalAttempted bool
	dial             func(context.Context, authstate.State) (*session, error)
	lease            *profileLease
	eventStream      chan events.Result
	eventDone        chan struct{}
	eventStop        chan struct{}
	commitMu         sync.Mutex
	commitActive     int
	commitDone       chan struct{}
	pendingCommits   map[int64][]int64
	readMu           sync.Mutex
	historyAdmission *historyAdmission
	historyStore     *historyStore
	historyResources *historyResourceCache
	liveMedia        liveMediaLane
	historyStartup   chan struct{}
	historyStartWait int
	gaps             gapFunc
}

// OpenOptions selects explicit bootstrap behavior for an operator-owned profile.
type OpenOptions struct {
	// HistoryPacing controls acquisition admission without changing read acknowledgements.
	HistoryPacing HistoryPacing
	// FullChatList requests a full inventory at the next login by using the
	// documented zero list token. Message commit positions and LBK are retained.
	FullChatList bool
	// PacketObserver, when set, sees every incoming LOCO packet on the read
	// loop before dispatch. It must not retain or modify the packet. Nil, the
	// default, adds no per-packet work. Research tooling such as the BSON
	// shadow comparison is wired in only through this field.
	PacketObserver func(loco.Packet)
	// GapObserver, when set, receives each message that decoded to an
	// events.MessageGap, an events.UnsupportedMessage, or a reply whose
	// attachment is neither absent nor a sticker, with the raw BSON it came
	// from. Nil, the default, adds no work. Debug tooling such as the bridge's
	// gap dump is wired in only through this field.
	GapObserver func(GapObservation)
}

// GapObservation is one message whose content failed decoding or has no
// decoder yet. Body is the LOCO body of a push, or the bare chat log of a
// SYNCMSG or GETMSGS read, and therefore contains private message content.
type GapObservation struct {
	// Source is the LOCO method the body arrived in.
	Source string
	// Gap is the decoded gap, or for a message that decoded but cannot be
	// displayed, its identity with one of the GapReasonUnsupported reasons.
	Gap  events.MessageGap
	Body []byte
}

// Observation reasons for messages that decoded but that mooo cannot display.
// Their raw body is the only record of the content's shape.
const (
	GapReasonUnsupportedType            = "unsupported_type"
	GapReasonUnsupportedReplyAttachment = "unsupported_reply_attachment"
)

// gapFunc reports an event to the GapObserver when it is a gap. A nil gapFunc
// is valid and does nothing.
type gapFunc func(source string, event events.Event, body []byte)

func (f gapFunc) observe(source string, event events.Event, body []byte) {
	if f != nil {
		f(source, event, body)
	}
}

func newGapFunc(observer func(GapObservation)) gapFunc {
	if observer == nil {
		return nil
	}
	return func(source string, event events.Event, body []byte) {
		var gap events.MessageGap
		switch event := event.(type) {
		case events.MessageGap:
			gap = event
		case events.UnsupportedMessage:
			gap = events.MessageGap{ChatID: event.ChatID, LogID: event.LogID, Type: event.Type, Reason: GapReasonUnsupportedType}
		case events.ReplyMessage:
			if event.Attachment.Type == 0 || event.Attachment.Sticker != nil {
				return
			}
			gap = events.MessageGap{ChatID: event.ChatID, LogID: event.LogID, AuthorID: event.AuthorID, SentAt: event.SentAt,
				Type: messagetype.Reply, Reason: GapReasonUnsupportedReplyAttachment}
		default:
			return
		}
		observer(GapObservation{Source: source, Gap: gap, Body: bytes.Clone(body)})
	}
}

// OpenWithOptions acquires the profile's process-wide owner lease and creates
// a reusable authenticated client without opening a network connection. It
// never creates a parallel secondary session or changes committed message
// positions.
func OpenWithOptions(statePath string, doer macweb.Doer, options OpenOptions) (*Client, error) {
	statePath = filepath.Clean(statePath)
	store, err := authstate.Open(statePath)
	if err != nil {
		return nil, err
	}
	lease, err := acquireProfileLease(statePath + ".lock")
	if err != nil {
		return nil, err
	}
	state, err := store.Snapshot()
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	client, err := newClient(state, doer)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	client.lease = lease
	client.store = store
	checkpoint, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	admission, err := openAccountHistoryAdmission(statePath, state.Credentials.UserID, options.HistoryPacing)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	history, err := openHistoryStore(statePath+".history-store", state.Credentials.UserID, state.Identity.DeviceUUID)
	if err != nil {
		admission.stop()
		_ = lease.Close()
		return nil, err
	}
	resources, err := openHistoryResourceCache(statePath+".history-resources", history.binding)
	if err != nil {
		admission.stop()
		_ = lease.Close()
		return nil, err
	}
	client.historyResources = resources
	client.historyStore = history
	client.historyAdmission = admission
	client.http = historyDependencyDoer{base: client.http}
	client.checkpoint = checkpoint
	client.gaps = newGapFunc(options.GapObserver)
	resume := options.loginResume(checkpoint.Snapshot())
	client.dial = func(ctx context.Context, state authstate.State) (*session, error) {
		return connectSession(ctx, state, sessionConfig{resume: resume, dialers: productionSessionDialers().observed(options.PacketObserver), ping: realtimePingOptions()})
	}
	return client, nil
}

func newClient(state authstate.State, doer macweb.Doer) (*Client, error) {
	if state.Credentials == nil {
		return nil, errCredentialsAbsent
	}
	if _, err := state.Identity.WireDeviceUUID(); err != nil {
		return nil, stageErr(errBootstrap, "device uuid", err)
	}
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	credentials := state.Credentials.Clone()
	state.Credentials = &credentials
	dial := func(ctx context.Context, state authstate.State) (*session, error) {
		return connectSession(ctx, state, sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: productionSessionDialers(), ping: realtimePingOptions()})
	}
	return &Client{state: state, http: doer, dial: dial}, nil
}

// Connect establishes the client's session if needed. Repeated calls after
// success are idempotent and reuse the existing session.
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.ensureSession(ctx)
	return err
}

// withSession lazily establishes the Client's one Session and runs fn on it.
// Client operations reach the Session only through it.
func withSession[T any](c *Client, ctx context.Context, fn func(*session) (T, error)) (T, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return fn(session)
}

func (c *Client) ensureSession(ctx context.Context) (*session, error) {
	if c == nil || ctx == nil {
		return nil, ErrProtocol
	}
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrClientClosed
		}
		if c.session != nil {
			session := c.session
			c.mu.Unlock()
			return session, nil
		}
		if c.connectActive {
			done := c.connectDone
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		c.connectActive = true
		c.connectDone = make(chan struct{})
		c.mu.Unlock()

		session, err := c.connect(ctx)
		c.mu.Lock()
		c.connectActive = false
		close(c.connectDone)
		c.connectDone = nil
		c.mu.Unlock()
		return session, err
	}
}

// connect dials and adopts one Session for ensureSession, which owns the
// connectActive gate. A -950 dial status renews stored credentials at most
// once per Client and dials again.
func (c *Client) connect(ctx context.Context) (*session, error) {
	c.mu.Lock()
	state, dial := c.state, c.dial
	c.mu.Unlock()
	session, err := dial(ctx, state)
	if err == nil {
		return c.adoptSession(session)
	}
	var status StatusError
	c.mu.Lock()
	store, renewalAttempted, closed := c.store, c.renewalAttempted, c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClientClosed
	}
	if store == nil || renewalAttempted || !errors.As(err, &status) || status.Status != -950 {
		return nil, err
	}
	c.mu.Lock()
	c.renewalAttempted = true
	c.mu.Unlock()
	if renewErr := c.renewCredentials(ctx); renewErr != nil {
		return nil, errors.Join(ErrCredentialRenewal, renewErr)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	state, dial = c.state, c.dial
	c.mu.Unlock()
	session, err = dial(ctx, state)
	if err != nil {
		return nil, err
	}
	return c.adoptSession(session)
}

// adoptSession installs a freshly dialed Session's login cursor into the
// continuity checkpoint and publishes it as the Client's session. A checkpoint
// failure closes the Client; a Client closed during the dial gets the Session
// recorded so Shutdown can join its worker, since closing the transport alone
// is not a worker-join guarantee.
func (c *Client) adoptSession(session *session) (*session, error) {
	c.mu.Lock()
	checkpoint := c.checkpoint
	c.mu.Unlock()
	if checkpoint != nil {
		cursor := session.loginCursor
		if err := checkpoint.InstallSession(cursor.lastTokenID, cursor.lbk, cursor.observed, cursor.deleted, cursor.replaceInventory); err != nil {
			c.mu.Lock()
			c.cleanupSession = session
			c.closed = true
			if c.historyAdmission != nil {
				c.historyAdmission.stop()
			}
			c.mu.Unlock()
			_ = session.Close()
			return nil, err
		}
	}
	c.mu.Lock()
	if c.closed {
		c.session = session
		c.mu.Unlock()
		_ = session.Close()
		return nil, ErrClientClosed
	}
	session.historyAdmission = c.historyAdmission
	c.session = session
	c.mu.Unlock()
	return session, nil
}

// initialChatData returns the synchronized login snapshot, connecting once if
// needed and reusing that connection for later operations.
func (c *Client) initialChatData(ctx context.Context) ([]bson.Raw, error) {
	return withSession(c, ctx, func(session *session) ([]bson.Raw, error) {
		return session.chatDataSnapshot(), nil
	})
}

// sessionPushes lazily establishes the session and returns its
// unsolicited-packet stream for Events.
func (c *Client) sessionPushes(ctx context.Context) (<-chan loco.Packet, error) {
	return withSession(c, ctx, func(session *session) (<-chan loco.Packet, error) {
		raw := session.pushStream()
		if raw == nil {
			return nil, ErrProtocol
		}
		return raw, nil
	})
}

// Events returns one reusable typed event stream for this client. Packet decode
// failures are emitted as Result errors without terminating the stream. Each
// session has one ordered unsolicited-packet consumer: this stream.
func (c *Client) Events(ctx context.Context) (<-chan events.Result, error) {
	raw, err := c.sessionPushes(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	// ensureSession may have returned immediately before Shutdown marked the
	// client closed. Recheck admission under the same lock before creating a
	// decoder worker, otherwise shutdown can release ownership while this late
	// Events call installs an unjoined goroutine.
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	if c.eventStream != nil {
		stream := c.eventStream
		c.mu.Unlock()
		return stream, nil
	}
	stream := make(chan events.Result, requestLimit)
	done := make(chan struct{})
	stop := make(chan struct{})
	c.eventStream = stream
	c.eventDone = done
	c.eventStop = stop
	checkpoint := c.checkpoint
	c.mu.Unlock()
	go func() {
		defer close(done)
		decodeEventStreamWithTerminalStop(raw, stream, checkpoint, c.queueCommit, c.interruptTerminal, stop, c.gaps, c.observeHistoryPacket)
	}()
	return stream, nil
}

// interruptTerminal closes admission and interrupts the owned Session after a
// typed terminal notice. It deliberately retains the checkpoint and profile
// lease; Shutdown must join any workers before releasing either resource.
func (c *Client) interruptTerminal() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	if c.historyAdmission != nil {
		c.historyAdmission.stop()
	}
	session := c.session
	c.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}

type messagePosition struct {
	chatID int64
	logID  int64
}

const observedPositionLimit = 4096

func decodeEventStreamWithTerminalStop(raw <-chan loco.Packet, output chan<- events.Result, checkpoint *continuity.Store, delivered func(int64, int64), terminal func(), stop <-chan struct{}, gaps gapFunc, observers ...func(loco.Packet, events.Event) error) {
	defer close(output)
	seen := make(map[messagePosition]struct{})
	order := make([]messagePosition, 0, observedPositionLimit)
	for {
		var packet loco.Packet
		var ok bool
		if stop == nil {
			packet, ok = <-raw
		} else {
			select {
			case packet, ok = <-raw:
			case <-stop:
				return
			}
		}
		if !ok {
			return
		}
		event, err := events.DecodeForDelivery(packet)
		if err == nil {
			for _, observe := range observers {
				if err = observe(packet, event); err != nil {
					break
				}
			}
			if err != nil {
				if terminal != nil {
					terminal()
				}
				_ = emitEventResult(output, events.Result{Err: err}, stop)
				return
			}
			gaps.observe(packet.Header.Method, event, packet.Body)
		}
		if err != nil && (packet.Header.Method == "MSG" || errors.Is(err, events.ErrUnidentifiableMembership)) {
			// Without a trustworthy position no later commit can prove that
			// it did not cross this message. Stop admission and retain cursors.
			if terminal != nil {
				terminal()
			}
			_ = emitEventResult(output, events.Result{Err: err}, stop)
			return
		}
		if err == nil {
			if chatID, logID, ok := events.MessagePosition(event); ok {
				position := messagePosition{chatID: chatID, logID: logID}
				if checkpoint != nil && checkpoint.IsCommitted(chatID, logID) {
					continue
				}
				if _, duplicate := seen[position]; duplicate {
					continue
				}
				if checkpoint != nil {
					if persistErr := checkpoint.RecordDeliveryStart(chatID, logID); persistErr != nil {
						if terminal != nil {
							terminal()
						}
						_ = emitEventResult(output, events.Result{Err: persistErr}, stop)
						return
					}
				}
				seen[position] = struct{}{}
				order = append(order, position)
				if len(order) > observedPositionLimit {
					delete(seen, order[0])
					order = order[1:]
				}
				if delivered != nil {
					delivered(chatID, logID)
				}
			}
		}
		if err == nil {
			switch event.(type) {
			case events.ChangeServer, events.Kickout:
				if terminal != nil {
					terminal()
				}
				if !emitEventResult(output, events.Result{Event: event, Err: err}, stop) {
					return
				}
				return
			}
		}
		if !emitEventResult(output, events.Result{Event: event, Err: err}, stop) {
			return
		}
	}
}

func emitEventResult(output chan<- events.Result, result events.Result, stop <-chan struct{}) bool {
	if stop == nil {
		output <- result
		return true
	}
	select {
	case output <- result:
		return true
	case <-stop:
		return false
	}
}

// CommitEvent advances the durable resume boundary after the application has
// successfully persisted or bridged one incoming message. It is intentionally
// explicit: receiving an event is not sufficient to prevent replay after a
// crash.
func (c *Client) CommitEvent(event events.Event) error {
	if c == nil || event == nil {
		return ErrProtocol
	}
	chatID, logID, ok := events.MessagePosition(event)
	if !ok {
		return ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	checkpoint := c.checkpoint
	endPersistence := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer endPersistence()
	if checkpoint == nil {
		return ErrProtocol
	}
	c.commitMu.Lock()
	defer c.commitMu.Unlock()
	queue := c.pendingCommits[chatID]
	if len(queue) == 0 || queue[0] != logID {
		return errCommitOrder
	}
	_, err := checkpoint.CommitMessage(chatID, logID)
	if err == nil {
		if len(queue) == 1 {
			delete(c.pendingCommits, chatID)
		} else {
			c.pendingCommits[chatID] = queue[1:]
		}
	}
	return err
}

// beginPersistenceLocked marks a checkpoint operation admitted before the
// caller releases Client.mu. Shutdown waits for all admitted operations before
// releasing profile ownership.
func (c *Client) beginPersistenceLocked() func() {
	if c.commitActive == 0 {
		c.commitDone = make(chan struct{})
	}
	c.commitActive++
	return func() {
		c.mu.Lock()
		c.commitActive--
		if c.commitActive == 0 {
			close(c.commitDone)
			c.commitDone = nil
		}
		c.mu.Unlock()
	}
}

func (c *Client) checkpointWrite(fn func(*continuity.Store) error) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	checkpoint := c.checkpoint
	if checkpoint == nil {
		c.mu.Unlock()
		return ErrProtocol
	}
	endPersistence := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer endPersistence()
	return fn(checkpoint)
}

func (c *Client) queueCommit(chatID, logID int64) {
	c.commitMu.Lock()
	defer c.commitMu.Unlock()
	if c.pendingCommits == nil {
		c.pendingCommits = make(map[int64][]int64)
	}
	c.pendingCommits[chatID] = append(c.pendingCommits[chatID], logID)
}

// CreateChat uses the client's existing session, or lazily establishes its
// first one. It does not reconnect or retry after any transport failure.
func (c *Client) CreateChat(ctx context.Context, request chat.CreateRequest) (chat.CreateResponse, error) {
	return withSession(c, ctx, func(session *session) (chat.CreateResponse, error) {
		return session.CreateChat(ctx, request)
	})
}

// AddMembers uses the existing profile session and sends the invitation once.
func (c *Client) AddMembers(ctx context.Context, request chat.AddMembersRequest) (chat.AddMembersResponse, error) {
	if _, err := request.MarshalBSON(); err != nil {
		return chat.AddMembersResponse{}, err
	}
	return withSession(c, ctx, func(session *session) (chat.AddMembersResponse, error) {
		return session.AddMembers(ctx, request)
	})
}

// SendText lazily connects once, then sends one text message without retrying
// an ambiguous transport result.
func (c *Client) SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error) {
	return withSession(c, ctx, func(session *session) (chat.WriteResponse, error) {
		return session.SendText(ctx, chatID, message)
	})
}

// SendReply lazily connects once, then sends one reply without retrying an
// ambiguous transport result.
func (c *Client) SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error) {
	return withSession(c, ctx, func(session *session) (chat.WriteResponse, error) {
		return session.SendReply(ctx, request)
	})
}

// SendImage lazily connects once, then sends one JPEG or PNG without retrying
// any ambiguous mutation or upload stage.
func (c *Client) SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error) {
	return withSession(c, ctx, func(session *session) (media.SendResult, error) {
		result, err := session.SendImage(ctx, chatID, data, caption)
		if !errors.Is(err, media.ErrUploadRedirect) {
			return result, err
		}
		return c.directPhoto(ctx, session, chatID, data, caption)
	})
}

// SendUpload sends one prepared file or video through the current session.
func (c *Client) SendUpload(ctx context.Context, chatID int64, upload media.Upload) (media.SendResult, error) {
	return withSession(c, ctx, func(session *session) (media.SendResult, error) {
		return session.SendUpload(ctx, chatID, upload)
	})
}

// SendAlbum sends 2 to 30 photos as one album through the current session.
func (c *Client) SendAlbum(ctx context.Context, chatID int64, photos [][]byte, caption string) (chat.WriteResponse, error) {
	return withSession(c, ctx, func(session *session) (chat.WriteResponse, error) {
		return session.SendAlbum(ctx, chatID, photos, caption)
	})
}

// httpProfile admits one authenticated HTTP call: it rejects a nil Client or
// Context and a closed Client, then snapshots the state and HTTP doer.
func (c *Client) httpProfile(ctx context.Context) (macweb.Doer, authstate.State, error) {
	if c == nil || ctx == nil {
		return nil, authstate.State{}, ErrProtocol
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, authstate.State{}, ErrClientClosed
	}
	return c.http, c.state, nil
}

// AddFriendByPhone performs the authenticated HTTP mutation without changing
// the LOCO session lifecycle.
func (c *Client) AddFriendByPhone(ctx context.Context, request friends.AddByPhoneRequest) (friends.Friend, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return friends.Friend{}, err
	}
	return addFriendByPhone(ctx, doer, state, request)
}

// React applies or cancels one reaction through the authenticated HTTP API.
// It sends the mutation exactly once and does not require a new LOCO login.
func (c *Client) React(ctx context.Context, request reactions.Request) (reactions.Response, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return reactions.Response{}, err
	}
	if request.RequestID == 0 {
		request.RequestID = time.Now().UnixMilli()
	}
	return sendReaction(ctx, doer, state, request)
}

// ReactionMembers returns the current user IDs grouped by reaction type for one
// message. It is a read-only HTTP lookup and does not open a LOCO session.
func (c *Client) ReactionMembers(ctx context.Context, chatID, logID int64) (reactions.MembersResponse, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return reactions.MembersResponse{}, err
	}
	return historyReadJSON(c, ctx, []any{"reaction-members", chatID, logID}, func(ctx context.Context) (reactions.MembersResponse, error) {
		return reactionMembers(ctx, doer, state, chatID, logID)
	})
}

// ReactionMetaSync reads one page of message metadata (reaction) changes
// newer than cur, without opening a LOCO session.
func (c *Client) ReactionMetaSync(ctx context.Context, chatID, cur int64) (reactions.SyncMetaPage, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return reactions.SyncMetaPage{}, err
	}
	profile, err := webProfile(state)
	if err != nil {
		return reactions.SyncMetaPage{}, err
	}
	return historyReadJSON(c, ctx, []any{"reaction-meta", chatID, cur}, func(ctx context.Context) (reactions.SyncMetaPage, error) {
		return reactions.FetchSyncMeta(ctx, doer, profile, chatID, cur)
	})
}

// MiniReactionDetails returns attribution for the separate mini/custom-
// reaction data source without opening a LOCO session.
func (c *Client) MiniReactionDetails(ctx context.Context, chatID, linkID, logID int64) (reactions.DetailsResponse, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return reactions.DetailsResponse{}, err
	}
	return historyReadJSON(c, ctx, []any{"mini-details", chatID, linkID, logID}, func(ctx context.Context) (reactions.DetailsResponse, error) {
		return miniReactionDetails(ctx, doer, state, chatID, linkID, logID)
	})
}

// ReactionDetails resolves and merges the attribution sources indicated by one
// aggregate reaction update, matching the current Mac client's detail model.
func (c *Client) ReactionDetails(ctx context.Context, change events.ReactionChanged) ([]reactions.Detail, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return nil, err
	}
	return mergedReactionDetails(ctx, doer, state, change, reactionReadCallbacks{members: c.ReactionMembers, mini: c.MiniReactionDetails})
}

// Shutdown marks the Client closed, interrupts its Session without holding
// Client.mu, and joins in-flight connect, commit and event-decoder work before
// releasing checkpoint and profile ownership. A deadline leaves ownership
// intact so the caller can retry Shutdown once that work becomes joinable.
func (c *Client) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return ErrProtocol
	}
	for {
		c.mu.Lock()
		if c.shutdownActive {
			done := c.shutdownDone
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		c.shutdownActive = true
		c.shutdownDone = make(chan struct{})
		c.closed = true
		if c.historyAdmission != nil {
			c.historyAdmission.stop()
		}
		session := c.session
		cleanup := c.cleanupSession
		if session == nil {
			session, cleanup = cleanup, nil
		}
		var checkpoint *continuity.Store
		var lease *profileLease
		connectDone := c.connectDone
		commitDone := c.commitDone
		done := c.shutdownDone
		c.mu.Unlock()
		var interruptErr error
		if session != nil {
			interruptErr = session.Close()
		}
		if cleanup != nil && cleanup != session {
			interruptErr = errors.Join(interruptErr, cleanup.Close())
		}

		finish := func() {
			c.mu.Lock()
			if c.shutdownDone == done {
				c.shutdownActive = false
				close(done)
				c.shutdownDone = nil
			}
			c.mu.Unlock()
		}

		if connectDone != nil {
			select {
			case <-connectDone:
			case <-ctx.Done():
				finish()
				return errors.Join(interruptErr, ctx.Err())
			}
		}
		if commitDone != nil {
			select {
			case <-commitDone:
			case <-ctx.Done():
				finish()
				return errors.Join(interruptErr, ctx.Err())
			}
		}
		c.mu.Lock()
		session = c.session
		cleanup = c.cleanupSession
		if session == nil {
			session, cleanup = cleanup, nil
		}
		checkpoint, lease = c.checkpoint, c.lease
		c.mu.Unlock()
		if session != nil {
			if err := session.Shutdown(ctx); err != nil {
				finish()
				return errors.Join(interruptErr, err)
			}
		}
		if cleanup != nil {
			if err := cleanup.Shutdown(ctx); err != nil {
				finish()
				return errors.Join(interruptErr, err)
			}
		}
		c.mu.Lock()
		eventDone := c.eventDone
		eventStop := c.eventStop
		c.eventStop = nil
		c.mu.Unlock()
		if eventStop != nil {
			close(eventStop)
		}
		if eventDone != nil {
			select {
			case <-eventDone:
			case <-ctx.Done():
				finish()
				return errors.Join(interruptErr, ctx.Err())
			}
		}
		if c.historyAdmission != nil {
			select {
			case <-c.historyAdmission.stoppedDone:
			case <-ctx.Done():
				finish()
				return errors.Join(interruptErr, ctx.Err())
			}
		}
		if checkpoint != nil {
			if err := checkpoint.MarkClean(); err != nil {
				finish()
				return errors.Join(interruptErr, err)
			}
		}
		if lease != nil {
			if err := lease.Close(); err != nil {
				finish()
				return errors.Join(interruptErr, err)
			}
		}
		c.mu.Lock()
		if c.session == session || c.cleanupSession == session {
			c.session = nil
			c.cleanupSession = nil
			c.eventDone = nil
			c.checkpoint = nil
			c.lease = nil
		}
		c.mu.Unlock()
		finish()
		return interruptErr
	}
}

func (o OpenOptions) loginResume(resume continuity.Checkpoint) continuity.Checkpoint {
	if o.FullChatList {
		resume.LastTokenID = 0
	}
	return resume
}
