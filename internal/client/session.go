// Package client composes the reviewed Kakao bootstrap, authenticated LOCO
// carriage, and request/response correlation into a reusable client session.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/booking"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	requestLimit = 64
	requestIDMin = 100000000
	requestIDMax = 200000000
)

var (
	errCredentialsAbsent = errors.New("client: credentials absent")
	errBootstrap         = errors.New("client: bootstrap failed")
	ErrLogin             = errors.New("client: login rejected")
	ErrClosed            = errors.New("client: session closed")
	ErrProtocol          = errors.New("client: protocol error")
)

type StatusError struct {
	Command string
	Status  int32
}

func (e StatusError) Error() string { return fmt.Sprintf("client: %s status %d", e.Command, e.Status) }

// lifecycleScheduler is an injected owner seam for the reviewed carriage
// request lifecycle. Implementations must only enqueue cancellation/scheduling;
// they must not synchronously reenter Session or execute timer/network
// callbacks. Session calls these methods while holding lifecycleMu. Session
// never creates a timer or chooses an initial keep-alive admission policy.
type lifecycleScheduler interface {
	// Queue methods only enqueue lifecycle work; they must not synchronously
	// reenter Session or execute timer/network callbacks while lifecycleMu is held.
	queueCancel()
	queueSchedule() bool
}

// lifecycleShutdown is the optional terminal hook a scheduler implements
// (pingTimerOwner does) so stopLifecycle can release its timer.
type lifecycleShutdown interface {
	shutdown()
}

// session owns one authenticated carriage. A background reader dispatches
// correlated responses and preserves unsolicited packets for the caller.
type session struct {
	mu                 sync.Mutex
	writeGateOnce      sync.Once
	writeGate          chan struct{}
	lifecycleMu        sync.Mutex
	wire               *wireConn
	nextID             uint32
	closed             bool
	closing            bool
	pushes             chan loco.Packet
	pending            map[uint32]pendingRequest
	lifecycleScheduler lifecycleScheduler
	lifecycleStopped   bool
	initialChatData    []bson.Raw
	userID             int64
	appVersion         string
	mediaDial          wireDialer
	loginCursor        loginCursor
	bootstrapDone      bool
	bootstrapPushes    []loco.Packet
	readLoopStarted    bool
}

func newSession(scheduler lifecycleScheduler) *session {
	return &session{
		pending:            make(map[uint32]pendingRequest),
		lifecycleScheduler: scheduler,
		bootstrapDone:      true,
	}
}

// queueSchedule and queueCancelRequest keep owner callbacks out of Session.mu.
// lifecycleMu serializes them with stopLifecycle so shutdown cannot be
// followed by an effective timer rearm.
func (s *session) queueSchedule() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleStopped || s.lifecycleScheduler == nil {
		return
	}
	s.lifecycleScheduler.queueSchedule()
}

func (s *session) queueCancelRequest() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleStopped || s.lifecycleScheduler == nil {
		return
	}
	s.lifecycleScheduler.queueCancel()
}

func (s *session) stopLifecycle() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleStopped {
		return
	}
	s.lifecycleStopped = true
	if s.lifecycleScheduler != nil {
		s.lifecycleScheduler.queueCancel()
		if owner, ok := s.lifecycleScheduler.(lifecycleShutdown); ok {
			owner.shutdown()
		}
	}
}

// pendingRequest is one outstanding request. Its unique ID is the pair
// (method, packet ID); the map key carries the packet ID.
type pendingRequest struct {
	method string
	result chan requestResult
}

type requestResult struct {
	packet loco.Packet
	err    error
}

type wireDialer func(context.Context, string, int) (*wireConn, error)

const defaultPingInterval = 180 * time.Second
const defaultPingRequestTimeout = 15 * time.Second

type pingSessionOptions struct {
	clock                  relativeTimer
	interval               time.Duration
	timeout                time.Duration
	beforeBootstrapRequest func()
}

type sessionDialers struct {
	tls    wireDialer
	secure wireDialer
}

func productionSessionDialers() sessionDialers {
	return sessionDialers{tls: dialTLS, secure: dialSecure}
}

// observed returns dialers whose connections hand every incoming packet to
// observer. A nil observer returns the dialers unchanged.
func (d sessionDialers) observed(observer func(loco.Packet)) sessionDialers {
	if observer == nil {
		return d
	}
	wrap := func(dial wireDialer) wireDialer {
		return func(ctx context.Context, host string, port int) (*wireConn, error) {
			wire, err := dial(ctx, host, port)
			if wire != nil {
				wire.observer = observer
			}
			return wire, err
		}
	}
	return sessionDialers{tls: wrap(d.tls), secure: wrap(d.secure)}
}

// sessionConfig selects how connectSession bootstraps one session: the login
// resume checkpoint, the transport dialers and the keepalive ping policy.
type sessionConfig struct {
	resume  continuity.Checkpoint
	dialers sessionDialers
	ping    pingSessionOptions
}

// realtimePingOptions is the production keepalive policy.
func realtimePingOptions() pingSessionOptions {
	return pingSessionOptions{clock: realtimeTimer{}, interval: defaultPingInterval, timeout: defaultPingRequestTimeout}
}

// connectSession performs GETCONF, CHECKIN, secure carriage setup, and
// LOGINLIST from client-owned state. Client owns and reuses the result.
func connectSession(ctx context.Context, state authstate.State, config sessionConfig) (*session, error) {
	resume, dialers, pingOptions := config.resume, config.dialers, config.ping
	if ctx == nil || state.Credentials == nil {
		return nil, errCredentialsAbsent
	}
	if dialers.tls == nil || dialers.secure == nil {
		return nil, errBootstrap
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return nil, errBootstrap
	}
	bookingBody, err := (booking.GetConfRequest{
		MCCMNC: "99999", Model: state.Identity.Metadata.DeviceModel, OS: "mac", UserID: state.Credentials.UserID,
	}).MarshalBSON()
	if err != nil {
		return nil, errBootstrap
	}
	bookingWire, err := dialers.tls(ctx, booking.Host, 443)
	if err != nil {
		return nil, errBootstrap
	}
	bookingReply, _, err := bookingWire.request(1, booking.GetConfCommand, bookingBody)
	_ = bookingWire.close()
	bookingStatus, statusErr := loco.Status(bookingReply)
	if err != nil || statusErr != nil || bookingStatus != 0 {
		return nil, errBootstrap
	}
	hosts, ports, err := booking.DecodeBookingTargets(bookingReply.Body)
	if err != nil {
		return nil, errBootstrap
	}

	checkinBody, err := (booking.CheckinRequest{
		UserID: state.Credentials.UserID, OS: "mac", NType: 0, AppVer: state.Identity.Metadata.AppVersion,
		MCCMNC: "99999", Lang: "en", CountryISO: "US", UseSub: true,
	}).MarshalBSON()
	if err != nil {
		return nil, errBootstrap
	}
	checkinReply, _, err := checkin(ctx, hosts, ports, checkinBody, dialers)
	if err != nil {
		return nil, errBootstrap
	}
	carriageHost, carriagePort, err := booking.DecodeCheckinEndpoint(checkinReply.Body)
	if err != nil {
		return nil, errBootstrap
	}
	carriage, err := dialers.secure(ctx, carriageHost, carriagePort)
	if err != nil {
		return nil, errBootstrap
	}
	if pingOptions.timeout <= 0 {
		pingOptions.timeout = defaultPingRequestTimeout
	}
	var session *session
	var owner lifecycleScheduler
	if pingOptions.clock != nil && pingOptions.interval > 0 {
		owner = newPingTimerOwner(pingOptions.clock, pingOptions.interval, func() {
			if session == nil {
				return
			}
			pingCtx, cancel := context.WithTimeout(context.Background(), pingOptions.timeout)
			defer cancel()
			if _, err := session.request(pingCtx, "PING", []byte{5, 0, 0, 0, 0}); err != nil {
				_ = session.Close()
			}
		})
	}
	session = newSession(owner)
	session.wire = carriage
	session.bootstrapDone = false
	session.bootstrapPushes = make([]loco.Packet, 0)
	session.nextID = 2
	session.pushes = make(chan loco.Packet, requestLimit)
	session.userID = state.Credentials.UserID
	session.appVersion = state.Identity.Metadata.AppVersion
	session.mediaDial = dialers.secure
	cleanup := true
	defer func() {
		if cleanup {
			_ = session.Close()
		}
	}()

	chatIDs, maxIDs := resume.LoginCursors()
	loginBody, err := (sessionlogin.LoginListRequest{
		AppVer: state.Identity.Metadata.AppVersion, OS: "mac", Lang: "en", DUUID: wireUUID,
		OAuthToken: state.Credentials.AccessToken, NType: 0, MCCMNC: "99999", Revision: 0,
		DType: 2, PCST: 0, BG: false,
		ChatIDs: chatIDs, MaxIDs: maxIDs, LastTokenID: resume.LastTokenID, LBK: resume.LBK,
	}).MarshalBSON()
	if err != nil {
		return nil, errBootstrap
	}
	session.startReadLoop()
	loginReply, err := session.requestRaw(ctx, 2, "LOGINLIST", loginBody)
	if err != nil {
		return nil, ErrLogin
	}
	status, err := loco.Status(loginReply)
	if err != nil {
		return nil, ErrLogin
	}
	if !sessionlogin.ClassifyLoginStatus(status).Accepted() {
		return nil, StatusError{Command: "LOGINLIST", Status: status}
	}
	chatData, cursor, err := finishLoginSyncSession(ctx, session, loginReply.Body, pingOptions.beforeBootstrapRequest)
	if err != nil {
		return nil, ErrLogin
	}
	cursor.replaceInventory = resume.LastTokenID == 0 && cursor.complete
	session.initialChatData = chatData
	session.loginCursor = cursor
	if !session.finishBootstrap() {
		return nil, ErrClosed
	}
	_ = carriage.c.SetDeadline(time.Time{})
	cleanup = false
	return session, nil
}

// chatDataSnapshot returns independent copies of the chat-data documents
// obtained while completing LOGINLIST/LCHATLIST synchronization.
func (s *session) chatDataSnapshot() []bson.Raw {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]bson.Raw, len(s.initialChatData))
	for i, raw := range s.initialChatData {
		out[i] = append(bson.Raw(nil), raw...)
	}
	return out
}

// pushStream exposes unsolicited packets from the background reader, including
// packets received while no request is active. Callers should drain it
// continuously once mutations or subscriptions can produce events.
func (s *session) pushStream() <-chan loco.Packet {
	if s == nil {
		return nil
	}
	return s.pushes
}

// call marshals req, sends it once with request and decodes the successful
// reply body. A nil session or Context is rejected by request.
func call[Req interface{ MarshalBSON() ([]byte, error) }, Resp any](s *session, ctx context.Context, command string, req Req, decode func([]byte) (Resp, error)) (Resp, error) {
	var zero Resp
	body, err := req.MarshalBSON()
	if err != nil {
		return zero, err
	}
	reply, err := s.request(ctx, command, body)
	if err != nil {
		return zero, err
	}
	return decode(reply.Body)
}

// ignoreReply is a call decoder for commands whose reply carries nothing
// beyond its status.
func ignoreReply([]byte) (struct{}, error) { return struct{}{}, nil }

// request sends exactly once. A timeout or disconnect is ambiguous and is
// returned without retrying the command. It rejects a nil Session or Context,
// so callers need no guard of their own unless they dereference s first.
func (s *session) request(ctx context.Context, command string, body []byte) (loco.Packet, error) {
	reply, err := s.requestRaw(ctx, 0, command, body)
	if err != nil {
		return loco.Packet{}, err
	}
	status, err := loco.Status(reply)
	if err != nil {
		return reply, ErrProtocol
	}
	if status != 0 {
		return reply, StatusError{Command: command, Status: status}
	}
	return reply, nil
}

func (s *session) requestRaw(ctx context.Context, id uint32, command string, body []byte) (loco.Packet, error) {
	if s == nil || ctx == nil || command == "" {
		return loco.Packet{}, ErrProtocol
	}
	s.mu.Lock()
	if s.closed || s.closing || s.wire == nil {
		s.mu.Unlock()
		return loco.Packet{}, ErrClosed
	}
	var err error
	if id == 0 {
		id, err = s.allocateRequestIDLocked()
		if err != nil {
			s.mu.Unlock()
			return loco.Packet{}, err
		}
	} else if id >= s.nextID {
		s.nextID = id + 1
		if s.nextID >= requestIDMax {
			s.nextID = requestIDMin
		}
	}
	if _, exists := s.pending[id]; exists {
		s.mu.Unlock()
		return loco.Packet{}, ErrProtocol
	}
	result := make(chan requestResult, 1)
	s.pending[id] = pendingRequest{method: command, result: result}
	wire := s.wire
	s.mu.Unlock()
	s.queueCancelRequest()
	if err := s.writeRequest(ctx, wire, id, command, body); err != nil {
		s.removePending(id, result)
		return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, err)
	}
	select {
	case outcome := <-result:
		if outcome.err != nil {
			return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, outcome.err)
		}
		return outcome.packet, nil
	case <-ctx.Done():
		s.removePending(id, result)
		return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, ctx.Err())
	}
}

// allocateRequestIDLocked returns one ID from the official bounded request
// range. The caller must hold s.mu. IDs still present in pending are skipped
// so wraparound cannot correlate a new request with an older callback.
func (s *session) allocateRequestIDLocked() (uint32, error) {
	if s.nextID < requestIDMin || s.nextID >= requestIDMax {
		s.nextID = requestIDMin
	}
	start := s.nextID
	for {
		id := s.nextID
		s.nextID++
		if s.nextID >= requestIDMax {
			s.nextID = requestIDMin
		}
		if _, pending := s.pending[id]; !pending {
			return id, nil
		}
		if s.nextID == start {
			return 0, ErrProtocol
		}
	}
}

func (s *session) writeRequest(ctx context.Context, wire *wireConn, id uint32, command string, body []byte) error {
	raw, err := (loco.Packet{Header: loco.Header{PacketID: id, Method: command, BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		return err
	}
	if wire.secure != nil {
		raw, err = wire.secure.Encrypt(raw)
		if err != nil {
			return err
		}
	}
	_, err = s.writeRawPayload(ctx, wire, raw)
	return err
}

func (s *session) writeRawPayload(ctx context.Context, wire *wireConn, raw []byte) (int, error) {
	if err := s.acquireWrite(ctx); err != nil {
		return 0, err
	}
	defer s.releaseWrite()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	closed := s.closed || s.closing || s.wire != wire
	s.mu.Unlock()
	if closed {
		return 0, ErrClosed
	}
	cancelled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = wire.c.SetWriteDeadline(time.Now())
		close(cancelled)
	})
	written := 0
	remaining := raw
	var err error
	for len(remaining) > 0 {
		var n int
		n, err = wire.c.Write(remaining)
		if n > 0 {
			written += n
			remaining = remaining[n:]
		}
		if err != nil {
			break
		}
		if n <= 0 {
			err = fmt.Errorf("zero-byte write")
			break
		}
	}
	ctxErr := ctx.Err()
	if !stopCancel() {
		// The cancellation callback has started; wait for it so its deadline
		// cannot land after the reset below.
		<-cancelled
	}
	_ = wire.c.SetWriteDeadline(time.Time{})
	// A cancellation after the complete frame was written must not tear down
	// an otherwise reusable carriage. A write error after any bytes have been
	// sent leaves framing ambiguous and requires fail-closed cleanup.
	if err != nil && written > 0 {
		s.mu.Lock()
		if s.wire == wire {
			s.closing = true
		}
		s.mu.Unlock()
		_ = wire.close()
		s.stopLifecycle()
	}
	if ctxErr != nil {
		return written, ctxErr
	}
	return written, err
}

func (s *session) acquireWrite(ctx context.Context) error {
	s.writeGateOnce.Do(func() {
		s.writeGate = make(chan struct{}, 1)
		s.writeGate <- struct{}{}
	})
	select {
	case <-s.writeGate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) releaseWrite() {
	s.writeGate <- struct{}{}
}

func (s *session) removePending(id uint32, expected chan requestResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[id].result == expected {
		delete(s.pending, id)
	}
}

func (s *session) startReadLoop() {
	s.mu.Lock()
	if s.readLoopStarted {
		s.mu.Unlock()
		return
	}
	s.readLoopStarted = true
	s.mu.Unlock()
	go s.readLoopBody()
}

func (s *session) readLoopBody() {
	for {
		packet, err := s.wire.read()
		if err != nil {
			s.finishRead(err)
			return
		}
		if s.dispatchPacket(packet) {
			continue
		}
		// dispatchPacket reports unsolicited packets through the normal push path.
		s.mu.Lock()
		if !s.bootstrapDone && s.bootstrapPushes != nil {
			s.bootstrapPushes = append(s.bootstrapPushes, packet)
			s.mu.Unlock()
			continue
		}
		pushes := s.pushes
		s.mu.Unlock()
		select {
		case pushes <- packet:
		default:
			s.finishRead(ErrProtocol)
			_ = s.wire.close()
			return
		}
	}
}

// dispatchPacket correlates by the packet header unique ID, which is the
// pair (method, packet ID). A packet that reuses a pending ID with a different
// method is not a completion and stays on the push path.
func (s *session) dispatchPacket(packet loco.Packet) bool {
	s.mu.Lock()
	request, ok := s.pending[packet.Header.PacketID]
	ok = ok && request.method == packet.Header.Method
	if ok {
		delete(s.pending, packet.Header.PacketID)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.queueSchedule()
	request.result <- requestResult{packet: packet}
	return true
}

func (s *session) finishBootstrap() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.closing {
		return false
	}
	pushBuffer := requestLimit
	if len(s.bootstrapPushes) > pushBuffer {
		pushBuffer = len(s.bootstrapPushes)
	}
	pushes := make(chan loco.Packet, pushBuffer)
	for _, packet := range s.bootstrapPushes {
		pushes <- packet
	}
	s.bootstrapPushes = nil
	s.pushes = pushes
	s.bootstrapDone = true
	return true
}

func (s *session) finishRead(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	shouldSchedule := !s.closing
	pending := s.pending
	s.pending = nil
	pushes := s.pushes
	s.mu.Unlock()
	if shouldSchedule {
		for range pending {
			s.queueSchedule()
		}
	}
	s.stopLifecycle()
	for _, request := range pending {
		request.result <- requestResult{err: err}
	}
	if pushes != nil {
		close(pushes)
	}
}

func (s *session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	wire := s.wire
	if wire == nil {
		s.closed = true
		s.mu.Unlock()
		s.stopLifecycle()
		return nil
	}
	s.mu.Unlock()
	s.stopLifecycle()
	return wire.close()
}

// Shutdown interrupts the Session. It is currently equivalent to Close apart
// from rejecting a nil context.
func (s *session) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return context.Canceled
	}
	return s.Close()
}
