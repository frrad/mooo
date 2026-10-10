// Package client composes the reviewed Kakao bootstrap, authenticated LOCO
// carriage, and request/response correlation into a reusable client session.
package client

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
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
	ErrCredentialsAbsent = errors.New("client: credentials absent")
	ErrBootstrap         = errors.New("client: bootstrap failed")
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

// Session owns one authenticated carriage. A background reader dispatches
// correlated responses and preserves unsolicited packets for the caller.
type Session struct {
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

func newSession(scheduler lifecycleScheduler) *Session {
	return &Session{
		pending:            make(map[uint32]pendingRequest),
		lifecycleScheduler: scheduler,
		bootstrapDone:      true,
	}
}

// queueSchedule and queueCancelRequest keep owner callbacks out of Session.mu.
// lifecycleMu serializes them with stopLifecycle so shutdown cannot be
// followed by an effective timer rearm.
func (s *Session) queueSchedule() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleStopped || s.lifecycleScheduler == nil {
		return
	}
	s.lifecycleScheduler.queueSchedule()
}

func (s *Session) queueCancelRequest() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleStopped || s.lifecycleScheduler == nil {
		return
	}
	s.lifecycleScheduler.queueCancel()
}

func (s *Session) stopLifecycle() {
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

// connectSession performs GETCONF, CHECKIN, secure carriage setup, and
// LOGINLIST from client-owned state. Client owns and reuses the result.
func connectSession(ctx context.Context, state authstate.State) (*Session, error) {
	return connectSessionWithResumeOptions(ctx, state, continuity.Checkpoint{Version: continuity.Version}, productionSessionDialers(), pingSessionOptions{clock: realtimeTimer{}, interval: defaultPingInterval, timeout: defaultPingRequestTimeout})
}

func connectSessionWithResume(ctx context.Context, state authstate.State, resume continuity.Checkpoint, dialers sessionDialers) (*Session, error) {
	return connectSessionWithResumeOptions(ctx, state, resume, dialers, pingSessionOptions{clock: realtimeTimer{}, interval: defaultPingInterval, timeout: defaultPingRequestTimeout})
}

func connectSessionWithResumeOptions(ctx context.Context, state authstate.State, resume continuity.Checkpoint, dialers sessionDialers, pingOptions pingSessionOptions) (*Session, error) {
	if ctx == nil || state.Credentials == nil {
		return nil, ErrCredentialsAbsent
	}
	if dialers.tls == nil || dialers.secure == nil {
		return nil, ErrBootstrap
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return nil, ErrBootstrap
	}
	bookingBody, err := (booking.GetConfRequest{
		MCCMNC: "99999", Model: state.Identity.Metadata.DeviceModel, OS: "mac", UserID: state.Credentials.UserID,
	}).MarshalBSON()
	if err != nil {
		return nil, ErrBootstrap
	}
	bookingWire, err := dialers.tls(ctx, booking.Host, 443)
	if err != nil {
		return nil, ErrBootstrap
	}
	bookingReply, _, err := bookingWire.request(1, booking.GetConfCommand, bookingBody)
	_ = bookingWire.close()
	bookingStatus, statusErr := loco.Status(bookingReply)
	if err != nil || statusErr != nil || bookingStatus != 0 {
		return nil, ErrBootstrap
	}
	hosts, ports, err := booking.DecodeBookingTargets(bookingReply.Body)
	if err != nil {
		return nil, ErrBootstrap
	}

	checkinBody, err := (booking.CheckinRequest{
		UserID: state.Credentials.UserID, OS: "mac", NType: 0, AppVer: state.Identity.Metadata.AppVersion,
		MCCMNC: "99999", Lang: "en", CountryISO: "US", UseSub: true,
	}).MarshalBSON()
	if err != nil {
		return nil, ErrBootstrap
	}
	checkinReply, _, err := checkin(ctx, hosts, ports, checkinBody, dialers)
	if err != nil {
		return nil, ErrBootstrap
	}
	carriageHost, carriagePort, err := booking.DecodeCheckinEndpoint(checkinReply.Body)
	if err != nil {
		return nil, ErrBootstrap
	}
	carriage, err := dialers.secure(ctx, carriageHost, carriagePort)
	if err != nil {
		return nil, ErrBootstrap
	}
	if pingOptions.timeout <= 0 {
		pingOptions.timeout = defaultPingRequestTimeout
	}
	var session *Session
	var owner lifecycleScheduler
	if pingOptions.clock != nil && pingOptions.interval > 0 {
		owner = newPingTimerOwner(pingOptions.clock, pingOptions.interval, func() {
			if session == nil {
				return
			}
			pingCtx, cancel := context.WithTimeout(context.Background(), pingOptions.timeout)
			defer cancel()
			if _, err := session.Request(pingCtx, "PING", []byte{5, 0, 0, 0, 0}); err != nil {
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
		return nil, ErrBootstrap
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

// InitialChatData returns independent copies of the chat-data documents
// obtained while completing LOGINLIST/LCHATLIST synchronization.
func (s *Session) InitialChatData() []bson.Raw {
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

// Pushes exposes unsolicited packets from the background reader, including
// packets received while no request is active. Callers should drain it
// continuously once mutations or subscriptions can produce events.
func (s *Session) Pushes() <-chan loco.Packet {
	if s == nil {
		return nil
	}
	return s.pushes
}

// call marshals req, sends it once with Request and decodes the successful
// reply body. A nil Session or Context is rejected by Request.
func call[Req interface{ MarshalBSON() ([]byte, error) }, Resp any](s *Session, ctx context.Context, command string, req Req, decode func([]byte) (Resp, error)) (Resp, error) {
	var zero Resp
	body, err := req.MarshalBSON()
	if err != nil {
		return zero, err
	}
	reply, err := s.Request(ctx, command, body)
	if err != nil {
		return zero, err
	}
	return decode(reply.Body)
}

// ignoreReply is a call decoder for commands whose reply carries nothing
// beyond its status.
func ignoreReply([]byte) (struct{}, error) { return struct{}{}, nil }

// Request sends exactly once. A timeout or disconnect is ambiguous and is
// returned without retrying the command. It rejects a nil Session or Context,
// so callers need no guard of their own unless they dereference s first.
func (s *Session) Request(ctx context.Context, command string, body []byte) (loco.Packet, error) {
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

func (s *Session) requestRaw(ctx context.Context, id uint32, command string, body []byte) (loco.Packet, error) {
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
func (s *Session) allocateRequestIDLocked() (uint32, error) {
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

func (s *Session) writeRequest(ctx context.Context, wire *wireConn, id uint32, command string, body []byte) error {
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

func (s *Session) writeRawPayload(ctx context.Context, wire *wireConn, raw []byte) (int, error) {
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

func (s *Session) acquireWrite(ctx context.Context) error {
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

func (s *Session) releaseWrite() {
	s.writeGate <- struct{}{}
}

func (s *Session) removePending(id uint32, expected chan requestResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[id].result == expected {
		delete(s.pending, id)
	}
}

func (s *Session) startReadLoop() {
	s.mu.Lock()
	if s.readLoopStarted {
		s.mu.Unlock()
		return
	}
	s.readLoopStarted = true
	s.mu.Unlock()
	go s.readLoopBody()
}

func (s *Session) readLoopBody() {
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
func (s *Session) dispatchPacket(packet loco.Packet) bool {
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

func (s *Session) finishBootstrap() bool {
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

func (s *Session) finishRead(err error) {
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

func (s *Session) Close() error {
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
func (s *Session) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return context.Canceled
	}
	return s.Close()
}

type wireConn struct {
	c        net.Conn
	secure   *loco.SecureV3
	producer *loco.Producer
	// observer, when set, sees every incoming packet before dispatch. It is
	// OpenOptions.PacketObserver; nil costs nothing.
	observer func(loco.Packet)
}

func dialTLS(ctx context.Context, host string, port int) (*wireConn, error) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	return &wireConn{c: c}, nil
}

func dialSecure(ctx context.Context, host string, port int) (*wireConn, error) {
	c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	secure, err := loco.NewSecureV3(nil, 0)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	handshake, err := secure.Handshake()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if _, err := c.Write(handshake); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &wireConn{c: c, secure: secure}, nil
}

func (w *wireConn) close() error { return w.c.Close() }

func (w *wireConn) request(id uint32, method string, body []byte) (loco.Packet, []loco.Packet, error) {
	raw, err := (loco.Packet{Header: loco.Header{PacketID: id, Method: method, BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		return loco.Packet{}, nil, err
	}
	if w.secure != nil {
		raw, err = w.secure.Encrypt(raw)
		if err != nil {
			return loco.Packet{}, nil, err
		}
	}
	if _, err := w.c.Write(raw); err != nil {
		return loco.Packet{}, nil, err
	}
	var unsolicited []loco.Packet
	for range requestLimit {
		packet, err := w.read()
		if err != nil {
			return loco.Packet{}, unsolicited, err
		}
		if packet.Header.PacketID == id && packet.Header.Method == method {
			return packet, unsolicited, nil
		}
		unsolicited = append(unsolicited, packet)
	}
	return loco.Packet{}, unsolicited, ErrProtocol
}

// read is the single point every incoming LOCO packet passes through, so the
// optional packet observer runs here.
func (w *wireConn) read() (loco.Packet, error) {
	packet, err := w.readFrame()
	if err == nil && w.observer != nil {
		w.observer(packet)
	}
	return packet, err
}

func (w *wireConn) readFrame() (loco.Packet, error) {
	// Exact plain-transport reads naturally preserve split headers/bodies and
	// leave any coalesced following frame for the next call.
	if w.secure == nil {
		headerBytes := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(w.c, headerBytes); err != nil {
			return loco.Packet{}, err
		}
		header, err := loco.ParseHeader(headerBytes, 0)
		if err != nil {
			return loco.Packet{}, err
		}
		body := make([]byte, int(header.BodyLen))
		if _, err := io.ReadFull(w.c, body); err != nil {
			return loco.Packet{}, err
		}
		return loco.Packet{Header: header, Body: body}, nil
	}
	if w.producer == nil {
		w.producer = loco.NewProducer(0, 0)
	}
	for {
		if packet, ok, err := w.producer.Next(); err != nil {
			return loco.Packet{}, err
		} else if ok {
			return packet, nil
		}

		prefix := make([]byte, 4)
		if _, err := io.ReadFull(w.c, prefix); err != nil {
			if errors.Is(err, io.EOF) && w.producer.Buffered() > 0 {
				return loco.Packet{}, io.ErrUnexpectedEOF
			}
			return loco.Packet{}, err
		}
		n := binary.LittleEndian.Uint32(prefix)
		if n == 0 {
			continue
		}
		if n > loco.DefaultMaxCiphertext {
			return loco.Packet{}, loco.ErrCiphertextTooLarge
		}
		envelope := make([]byte, 4+int(n))
		copy(envelope, prefix)
		if _, err := io.ReadFull(w.c, envelope[4:]); err != nil {
			return loco.Packet{}, err
		}
		plaintext, err := w.secure.Decrypt(envelope)
		if err != nil {
			return loco.Packet{}, err
		}
		if err := w.producer.Append(plaintext); err != nil {
			return loco.Packet{}, err
		}
	}
}
