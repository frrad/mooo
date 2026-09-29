package client

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/friends"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/reactions"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrClientClosed = errors.New("client: client closed")

var ErrPushConsumerSelected = errors.New("client: push consumer already selected")

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
	state            authstate.State
	store            *authstate.Store
	http             friends.Doer
	session          *Session
	closed           bool
	renewalAttempted bool
	dial             func(context.Context, authstate.State) (*Session, error)
	lease            *profileLease
	pushConsumer     pushConsumerMode
	eventStream      chan events.Result
}

// Open acquires the profile's process-wide owner lease and creates a reusable
// authenticated client without opening a network connection.
func Open(statePath string, doer friends.Doer) (*Client, error) {
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
	return client, nil
}

func newClient(state authstate.State, doer friends.Doer) (*Client, error) {
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClientClosed
	}
	if c.session != nil {
		return c.session, nil
	}
	session, err := c.dial(ctx, c.state)
	if err == nil {
		c.session = session
		return session, nil
	}
	var status StatusError
	if c.store == nil || c.renewalAttempted || !errors.As(err, &status) || status.Status != -950 {
		return nil, err
	}
	c.renewalAttempted = true
	if renewErr := c.renewCredentials(ctx); renewErr != nil {
		return nil, errors.Join(ErrCredentialRenewal, renewErr)
	}
	session, err = c.dial(ctx, c.state)
	if err != nil {
		return nil, err
	}
	c.session = session
	return session, nil
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
// session disconnects; Client never reconnects it implicitly.
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
	c.pushConsumer = pushConsumerTyped
	c.eventStream = stream
	c.mu.Unlock()
	go decodeEventStream(raw, stream)
	return stream, nil
}

func decodeEventStream(raw <-chan loco.Packet, output chan<- events.Result) {
	defer close(output)
	for packet := range raw {
		event, err := events.Decode(packet)
		output <- events.Result{Event: event, Err: err}
	}
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
func (c *Client) SendImage(ctx context.Context, chatID int64, data []byte) (media.SendResult, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return media.SendResult{}, err
	}
	return session.SendImage(ctx, chatID, data)
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
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var err error
	if c.session != nil {
		err = c.session.Close()
	}
	if c.lease != nil {
		err = errors.Join(err, c.lease.Close())
	}
	return err
}
