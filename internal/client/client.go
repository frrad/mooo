package client

import (
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
	"github.com/frrad/mooo/internal/protocol/reactions"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrClientClosed = errors.New("client: client closed")

var ErrPushConsumerSelected = errors.New("client: push consumer already selected")

var ErrCommitOrder = errors.New("client: event commit is not the next delivered message for its chat")

type pushConsumerMode uint8

const (
	pushConsumerNone pushConsumerMode = iota
	pushConsumerRaw
	pushConsumerTyped
)

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
	session          *Session
	cleanupSession   *Session
	closed           bool
	renewalAttempted bool
	dial             func(context.Context, authstate.State) (*Session, error)
	lease            *profileLease
	pushConsumer     pushConsumerMode
	eventStream      chan events.Result
	eventDone        chan struct{}
	eventStop        chan struct{}
	commitMu         sync.Mutex
	commitActive     int
	commitDone       chan struct{}
	pendingCommits   map[int64][]int64
	readMu           sync.Mutex
}

// Open acquires the profile's process-wide owner lease and creates a reusable
// authenticated client without opening a network connection.
func Open(statePath string, doer macweb.Doer) (*Client, error) {
	return OpenWithOptions(statePath, doer, OpenOptions{})
}

// OpenOptions selects explicit bootstrap behavior for an operator-owned profile.
type OpenOptions struct {
	// FullChatList requests a full inventory at the next login by using the
	// documented zero list token. Message commit positions and LBK are retained.
	FullChatList bool
}

// OpenWithOptions acquires the same exclusive profile lease as Open. It never
// creates a parallel secondary session or changes committed message positions.
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
	client.checkpoint = checkpoint
	resume := options.loginResume(checkpoint.Snapshot())
	client.dial = func(ctx context.Context, state authstate.State) (*Session, error) {
		return connectSessionWithResume(ctx, state, resume, productionSessionDialers())
	}
	return client, nil
}

func newClient(state authstate.State, doer macweb.Doer) (*Client, error) {
	if state.Credentials == nil {
		return nil, ErrCredentialsAbsent
	}
	if _, err := state.Identity.WireDeviceUUID(); err != nil {
		return nil, ErrBootstrap
	}
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	credentials := state.Credentials.Clone()
	state.Credentials = &credentials
	return &Client{state: state, http: doer, dial: connectSession}, nil
}

// Connect establishes the client's session if needed. Repeated calls after
// success are idempotent and reuse the existing session.
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.ensureSession(ctx)
	return err
}

func (c *Client) ensureSession(ctx context.Context) (*Session, error) {
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
		state := c.state
		dial := c.dial
		c.mu.Unlock()

		finish := func() {
			c.mu.Lock()
			c.connectActive = false
			close(c.connectDone)
			c.connectDone = nil
			c.mu.Unlock()
		}
		session, err := dial(ctx, state)
		if err == nil {
			c.mu.Lock()
			checkpoint := c.checkpoint
			c.mu.Unlock()
			if checkpoint != nil {
				if checkpointErr := checkpoint.InstallSession(session.loginCursor.lastTokenID, session.loginCursor.lbk, session.loginCursor.observed, session.loginCursor.deleted, session.loginCursor.replaceInventory); checkpointErr != nil {
					c.mu.Lock()
					c.cleanupSession = session
					c.closed = true
					c.mu.Unlock()
					_ = session.Close()
					finish()
					return nil, checkpointErr
				}
			}
			c.mu.Lock()
			if c.closed {
				// Keep a late session reachable so Shutdown can join its worker;
				// closing the transport alone is not a worker-join guarantee.
				c.session = session
				c.mu.Unlock()
				_ = session.Close()
				finish()
				return nil, ErrClientClosed
			}
			c.session = session
			c.mu.Unlock()
			finish()
			return session, nil
		}
		var status StatusError
		c.mu.Lock()
		store := c.store
		renewalAttempted := c.renewalAttempted
		closed := c.closed
		c.mu.Unlock()
		if closed {
			finish()
			return nil, ErrClientClosed
		}
		if store == nil || renewalAttempted || !errors.As(err, &status) || status.Status != -950 {
			finish()
			return nil, err
		}
		c.mu.Lock()
		c.renewalAttempted = true
		c.mu.Unlock()
		if renewErr := c.renewCredentials(ctx); renewErr != nil {
			finish()
			return nil, errors.Join(ErrCredentialRenewal, renewErr)
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			finish()
			return nil, ErrClientClosed
		}
		state = c.state
		dial = c.dial
		c.mu.Unlock()
		session, err = dial(ctx, state)
		if err != nil {
			finish()
			return nil, err
		}
		c.mu.Lock()
		checkpoint := c.checkpoint
		c.mu.Unlock()
		if checkpoint != nil {
			if checkpointErr := checkpoint.InstallSession(session.loginCursor.lastTokenID, session.loginCursor.lbk, session.loginCursor.observed, session.loginCursor.deleted, session.loginCursor.replaceInventory); checkpointErr != nil {
				c.mu.Lock()
				c.cleanupSession = session
				c.closed = true
				c.mu.Unlock()
				_ = session.Close()
				finish()
				return nil, checkpointErr
			}
		}
		c.mu.Lock()
		if c.closed {
			// Keep a late session reachable so Shutdown can join its worker.
			c.session = session
			c.mu.Unlock()
			_ = session.Close()
			finish()
			return nil, ErrClientClosed
		}
		c.session = session
		c.mu.Unlock()
		finish()
		return session, nil
	}
}

// InitialChatData returns the synchronized login snapshot, connecting once if
// needed and reusing that connection for later operations.
func (c *Client) InitialChatData(ctx context.Context) ([]bson.Raw, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.InitialChatData(), nil
}

// Pushes returns the live unsolicited-packet stream, including MSG events,
// after lazily establishing the one reusable session. The stream closes if the
// session disconnects; Client never reconnects it implicitly. Raw consumers
// receive terminal packets without typed terminal shutdown and must close the
// client explicitly after applying their own terminal policy.
func (c *Client) Pushes(ctx context.Context) (<-chan loco.Packet, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	raw := session.Pushes()
	if raw == nil {
		return nil, ErrProtocol
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pushConsumer == pushConsumerTyped {
		return nil, ErrPushConsumerSelected
	}
	c.pushConsumer = pushConsumerRaw
	return raw, nil
}

// Events returns one reusable typed event stream for this client. Packet decode
// failures are emitted as Result errors without terminating the stream. Raw
// Pushes and typed Events are mutually exclusive because each session has one
// ordered unsolicited-packet consumer.
func (c *Client) Events(ctx context.Context) (<-chan events.Result, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	raw := session.Pushes()
	if raw == nil {
		return nil, ErrProtocol
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
	if c.pushConsumer == pushConsumerRaw {
		c.mu.Unlock()
		return nil, ErrPushConsumerSelected
	}
	if c.eventStream != nil {
		stream := c.eventStream
		c.mu.Unlock()
		return stream, nil
	}
	stream := make(chan events.Result, requestLimit)
	done := make(chan struct{})
	stop := make(chan struct{})
	c.pushConsumer = pushConsumerTyped
	c.eventStream = stream
	c.eventDone = done
	c.eventStop = stop
	checkpoint := c.checkpoint
	c.mu.Unlock()
	go func() {
		defer close(done)
		decodeEventStreamWithTerminalStop(raw, stream, checkpoint, c.queueCommit, c.interruptTerminal, stop)
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

func decodeEventStreamWithTerminalStop(raw <-chan loco.Packet, output chan<- events.Result, checkpoint *continuity.Store, delivered func(int64, int64), terminal func(), stop <-chan struct{}) {
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
		return ErrCommitOrder
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
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chat.CreateResponse{}, err
	}
	return session.CreateChat(ctx, request)
}

// AddMembers uses the existing profile session and sends the invitation once.
func (c *Client) AddMembers(ctx context.Context, request chat.AddMembersRequest) (chat.AddMembersResponse, error) {
	if _, err := request.MarshalBSON(); err != nil {
		return chat.AddMembersResponse{}, err
	}
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chat.AddMembersResponse{}, err
	}
	return session.AddMembers(ctx, request)
}

// SendText lazily connects once, then sends one text message without retrying
// an ambiguous transport result.
func (c *Client) SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return session.SendText(ctx, chatID, message)
}

// SendReply lazily connects once, then sends one reply without retrying an
// ambiguous transport result.
func (c *Client) SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return session.SendReply(ctx, request)
}

// SendImage lazily connects once, then sends one JPEG or PNG without retrying
// any ambiguous mutation or upload stage.
func (c *Client) SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return media.SendResult{}, err
	}
	return session.SendImage(ctx, chatID, data, caption)
}

// SendUpload sends one prepared file or video through the current session.
func (c *Client) SendUpload(ctx context.Context, chatID int64, upload media.Upload) (media.SendResult, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return media.SendResult{}, err
	}
	return session.SendUpload(ctx, chatID, upload)
}

// SendAlbum sends 2 to 30 photos as one album through the current session.
func (c *Client) SendAlbum(ctx context.Context, chatID int64, photos [][]byte, caption string) (chat.WriteResponse, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return session.SendAlbum(ctx, chatID, photos, caption)
}

// AddFriendByPhone performs the authenticated HTTP mutation without changing
// the LOCO session lifecycle.
func (c *Client) AddFriendByPhone(ctx context.Context, request friends.AddByPhoneRequest) (friends.Friend, error) {
	if c == nil || ctx == nil {
		return friends.Friend{}, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return friends.Friend{}, ErrClientClosed
	}
	state, doer := c.state, c.http
	c.mu.Unlock()
	return addFriendByPhone(ctx, doer, state, request)
}

// React applies or cancels one reaction through the authenticated HTTP API.
// It sends the mutation exactly once and does not require a new LOCO login.
func (c *Client) React(ctx context.Context, request reactions.Request) (reactions.Response, error) {
	if c == nil || ctx == nil {
		return reactions.Response{}, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return reactions.Response{}, ErrClientClosed
	}
	state, doer := c.state, c.http
	c.mu.Unlock()
	if request.RequestID == 0 {
		request.RequestID = time.Now().UnixMilli()
	}
	return sendReaction(ctx, doer, state, request)
}

// ReactionMembers returns the current user IDs grouped by reaction type for one
// message. It is a read-only HTTP lookup and does not open a LOCO session.
func (c *Client) ReactionMembers(ctx context.Context, chatID, logID int64) (reactions.MembersResponse, error) {
	if c == nil || ctx == nil {
		return reactions.MembersResponse{}, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return reactions.MembersResponse{}, ErrClientClosed
	}
	state, doer := c.state, c.http
	c.mu.Unlock()
	return reactionMembers(ctx, doer, state, chatID, logID)
}

// ReactionMetaSync reads one page of message metadata (reaction) changes
// newer than cur, without opening a LOCO session.
func (c *Client) ReactionMetaSync(ctx context.Context, chatID, cur int64) (reactions.SyncMetaPage, error) {
	if c == nil || ctx == nil {
		return reactions.SyncMetaPage{}, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return reactions.SyncMetaPage{}, ErrClientClosed
	}
	state, doer := c.state, c.http
	c.mu.Unlock()
	profile, err := webProfile(state)
	if err != nil {
		return reactions.SyncMetaPage{}, err
	}
	return reactions.FetchSyncMeta(ctx, doer, profile, chatID, cur)
}

// MiniReactionDetails returns attribution for the separate mini/custom-
// reaction data source without opening a LOCO session.
func (c *Client) MiniReactionDetails(ctx context.Context, chatID, linkID, logID int64) (reactions.DetailsResponse, error) {
	if c == nil || ctx == nil {
		return reactions.DetailsResponse{}, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return reactions.DetailsResponse{}, ErrClientClosed
	}
	state, doer := c.state, c.http
	c.mu.Unlock()
	return miniReactionDetails(ctx, doer, state, chatID, linkID, logID)
}

// ReactionDetails resolves and merges the attribution sources indicated by one
// aggregate reaction update, matching the current Mac client's detail model.
func (c *Client) ReactionDetails(ctx context.Context, change events.ReactionChanged) ([]reactions.Detail, error) {
	if c == nil || ctx == nil {
		return nil, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	state, doer := c.state, c.http
	c.mu.Unlock()
	return mergedReactionDetails(ctx, doer, state, change)
}

// Close permanently closes this Client and its owned session.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	session, cleanup, checkpoint, lease := c.session, c.cleanupSession, c.checkpoint, c.lease
	if c.connectActive || c.commitActive > 0 {
		c.mu.Unlock()
		if session != nil {
			err := session.Close()
			if cleanup != nil && cleanup != session {
				err = errors.Join(err, cleanup.Close())
			}
			return err
		}
		return nil
	}
	c.mu.Unlock()
	var err error
	if session != nil {
		err = session.Close()
	}
	if cleanup != nil && cleanup != session {
		err = errors.Join(err, cleanup.Close())
	}
	if session != nil && checkpoint != nil {
		err = errors.Join(err, checkpoint.MarkClean())
	}
	if lease != nil {
		err = errors.Join(err, lease.Close())
	}
	return err
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
