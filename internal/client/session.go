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
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	bookingHost  = "booking-loco.kakao.com"
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
	pending            map[uint32]chan requestResult
	lifecycleScheduler lifecycleScheduler
	lifecycleStopped   bool
	initialChatData    []bson.Raw
	userID             int64
	appVersion         string
	mediaDial          wireDialer
	loginCursor        loginCursor
}

func newSession(scheduler lifecycleScheduler) *Session {
	return &Session{
		pending:            make(map[uint32]chan requestResult),
		lifecycleScheduler: scheduler,
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

type loginCursor struct {
	lastTokenID      *int64
	lbk              *int32
	observed         []continuity.ChatTarget
	deleted          []int64
	replaceInventory bool
}

type requestResult struct {
	packet loco.Packet
	err    error
}

type wireDialer func(context.Context, string, int) (*wireConn, error)

type sessionDialers struct {
	tls    wireDialer
	secure wireDialer
}

func productionSessionDialers() sessionDialers {
	return sessionDialers{tls: dialTLS, secure: dialSecure}
}

// connectSession performs GETCONF, CHECKIN, secure carriage setup, and
// LOGINLIST from client-owned state. Client owns and reuses the result.
func connectSession(ctx context.Context, state authstate.State) (*Session, error) {
	return connectSessionWithResume(ctx, state, continuity.Checkpoint{Version: continuity.Version}, productionSessionDialers())
}

func connectSessionWithDialers(ctx context.Context, state authstate.State, dialers sessionDialers) (*Session, error) {
	return connectSessionWithResume(ctx, state, continuity.Checkpoint{Version: continuity.Version}, dialers)
}

func connectSessionWithResume(ctx context.Context, state authstate.State, resume continuity.Checkpoint, dialers sessionDialers) (*Session, error) {
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
	bookingBody, err := bson.Marshal(bson.D{
		{Key: "MCCMNC", Value: "99999"},
		{Key: "model", Value: state.Identity.Metadata.DeviceModel},
		{Key: "os", Value: "mac"},
		{Key: "userId", Value: state.Credentials.UserID},
	})
	if err != nil {
		return nil, ErrBootstrap
	}
	booking, err := dialers.tls(ctx, bookingHost, 443)
	if err != nil {
		return nil, ErrBootstrap
	}
	bookingReply, _, err := booking.request(1, "GETCONF", bookingBody)
	_ = booking.close()
	bookingStatus, statusErr := responseStatus(bookingReply)
	if err != nil || statusErr != nil || bookingStatus != 0 {
		return nil, ErrBootstrap
	}
	hosts, ports, err := bookingTargets(bookingReply.Body)
	if err != nil {
		return nil, ErrBootstrap
	}

	checkinBody, err := bson.Marshal(bson.D{
		{Key: "userId", Value: state.Credentials.UserID},
		{Key: "os", Value: "mac"},
		{Key: "ntype", Value: int32(0)},
		{Key: "appVer", Value: state.Identity.Metadata.AppVersion},
		{Key: "MCCMNC", Value: "99999"},
		{Key: "lang", Value: "en"},
		{Key: "countryISO", Value: "US"},
		{Key: "useSub", Value: true},
	})
	if err != nil {
		return nil, ErrBootstrap
	}
	checkinReply, _, err := checkin(ctx, hosts, ports, checkinBody, dialers)
	if err != nil {
		return nil, ErrBootstrap
	}
	carriageHost, carriagePort, err := endpoint(checkinReply.Body)
	if err != nil {
		return nil, ErrBootstrap
	}
	carriage, err := dialers.secure(ctx, carriageHost, carriagePort)
	if err != nil {
		return nil, ErrBootstrap
	}

	chatIDs, maxIDs := resume.LoginCursors()
	loginBody, err := (sessionlogin.LoginListRequest{
		AppVer: state.Identity.Metadata.AppVersion, OS: "mac", Lang: "en", DUUID: wireUUID,
		OAuthToken: state.Credentials.AccessToken, NType: 0, MCCMNC: "99999", Revision: 0,
		DType: 2, PCST: 0, RP: []byte{0, 0, 0xff, 0xff, 0, 0}, BG: false,
		ChatIDs: chatIDs, MaxIDs: maxIDs, LastTokenID: resume.LastTokenID, LBK: resume.LBK,
	}).MarshalBSON()
	if err != nil {
		_ = carriage.close()
		return nil, ErrBootstrap
	}
	loginReply, _, err := carriage.request(2, "LOGINLIST", loginBody)
	if err != nil {
		_ = carriage.close()
		return nil, ErrLogin
	}
	status, err := responseStatus(loginReply)
	if err != nil {
		_ = carriage.close()
		return nil, ErrLogin
	}
	if !sessionlogin.ClassifyLoginStatus(status).Accepted() {
		_ = carriage.close()
		return nil, StatusError{Command: "LOGINLIST", Status: status}
	}
	chatData, nextID, pendingPushes, cursor, err := finishLoginSync(carriage, loginReply.Body, status, 3)
	if err != nil {
		_ = carriage.close()
		return nil, ErrLogin
	}
	cursor.replaceInventory = resume.LastTokenID == 0
	pushBuffer := requestLimit
	if len(pendingPushes) > pushBuffer {
		pushBuffer = len(pendingPushes)
	}
	session := newSession(nil)
	session.wire = carriage
	session.nextID = nextID
	session.pushes = make(chan loco.Packet, pushBuffer)
	session.initialChatData = chatData
	session.userID = state.Credentials.UserID
	session.appVersion = state.Identity.Metadata.AppVersion
	session.mediaDial = dialers.secure
	session.loginCursor = cursor
	for _, packet := range pendingPushes {
		session.pushes <- packet
	}
	_ = carriage.c.SetDeadline(time.Time{})
	go session.readLoop()
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

// Request sends exactly once. A timeout or disconnect is ambiguous and is
// returned without retrying the command.
func (s *Session) Request(ctx context.Context, command string, body []byte) (loco.Packet, error) {
	if s == nil || ctx == nil || command == "" {
		return loco.Packet{}, ErrProtocol
	}
	s.mu.Lock()
	if s.closed || s.closing || s.wire == nil {
		s.mu.Unlock()
		return loco.Packet{}, ErrClosed
	}
	id, err := s.allocateRequestIDLocked()
	if err != nil {
		s.mu.Unlock()
		return loco.Packet{}, err
	}
	result := make(chan requestResult, 1)
	s.pending[id] = result
	wire := s.wire
	s.mu.Unlock()
	s.queueCancelRequest()

	if err := s.writeRequest(ctx, wire, id, command, body); err != nil {
		s.removePending(id, result)
		return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, err)
	}
	var reply loco.Packet
	select {
	case outcome := <-result:
		if outcome.err != nil {
			return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, outcome.err)
		}
		reply = outcome.packet
	case <-ctx.Done():
		s.removePending(id, result)
		return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, ctx.Err())
	}
	status, err := responseStatus(reply)
	if err != nil {
		return reply, ErrProtocol
	}
	if status != 0 {
		return reply, StatusError{Command: command, Status: status}
	}
	return reply, nil
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
	if err := s.acquireWrite(ctx); err != nil {
		return err
	}
	defer s.releaseWrite()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed || s.closing || s.wire != wire
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = wire.c.SetWriteDeadline(time.Now())
		case <-stop:
		}
	}()
	written, err := writeAllCount(wire.c, raw)
	ctxErr := ctx.Err()
	close(stop)
	<-watchDone
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
		return ctxErr
	}
	return err
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
	if s.pending[id] == expected {
		delete(s.pending, id)
	}
}

func (s *Session) readLoop() {
	for {
		packet, err := s.wire.read()
		if err != nil {
			s.finishRead(err)
			return
		}
		s.mu.Lock()
		waiter := s.pending[packet.Header.PacketID]
		if waiter != nil {
			delete(s.pending, packet.Header.PacketID)
		}
		s.mu.Unlock()
		if waiter != nil {
			s.queueSchedule()
			waiter <- requestResult{packet: packet}
			continue
		}
		select {
		case s.pushes <- packet:
		default:
			s.finishRead(ErrProtocol)
			_ = s.wire.close()
			return
		}
	}
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
	for _, waiter := range pending {
		waiter <- requestResult{err: err}
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

type wireConn struct {
	c      net.Conn
	secure *loco.SecureV3
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
		if packet.Header.PacketID == id {
			return packet, unsolicited, nil
		}
		unsolicited = append(unsolicited, packet)
	}
	return loco.Packet{}, unsolicited, ErrProtocol
}

func (w *wireConn) read() (loco.Packet, error) {
	var plain []byte
	if w.secure != nil {
		prefix := make([]byte, 4)
		if _, err := io.ReadFull(w.c, prefix); err != nil {
			return loco.Packet{}, err
		}
		n := binary.LittleEndian.Uint32(prefix)
		if n > loco.DefaultMaxCiphertext {
			return loco.Packet{}, loco.ErrCiphertextTooLarge
		}
		envelope := make([]byte, 4+int(n))
		copy(envelope, prefix)
		if _, err := io.ReadFull(w.c, envelope[4:]); err != nil {
			return loco.Packet{}, err
		}
		var err error
		plain, err = w.secure.Decrypt(envelope)
		if err != nil {
			return loco.Packet{}, err
		}
	} else {
		header := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(w.c, header); err != nil {
			return loco.Packet{}, err
		}
		h, err := loco.ParseHeader(header, 0)
		if err != nil {
			return loco.Packet{}, err
		}
		plain = make([]byte, loco.HeaderSize+int(h.BodyLen))
		copy(plain, header)
		if _, err := io.ReadFull(w.c, plain[loco.HeaderSize:]); err != nil {
			return loco.Packet{}, err
		}
	}
	parser := loco.NewParser(0)
	packets, err := parser.Feed(plain)
	if err != nil || len(packets) != 1 {
		return loco.Packet{}, ErrProtocol
	}
	return packets[0], nil
}

func checkin(ctx context.Context, hosts []string, ports []int, body []byte, dialers sessionDialers) (loco.Packet, *wireConn, error) {
	for _, host := range hosts {
		for _, port := range append([]int{443}, ports...) {
			wire, err := dialers.tls(ctx, host, port)
			if err != nil {
				continue
			}
			reply, _, requestErr := wire.request(1, "CHECKIN", body)
			_ = wire.close()
			status, statusErr := responseStatus(reply)
			if requestErr == nil && statusErr == nil && status == 0 {
				return reply, wire, nil
			}
		}
		for _, port := range append(append([]int(nil), ports...), 995) {
			wire, err := dialers.secure(ctx, host, port)
			if err != nil {
				continue
			}
			reply, _, requestErr := wire.request(1, "CHECKIN", body)
			_ = wire.close()
			status, statusErr := responseStatus(reply)
			if requestErr == nil && statusErr == nil && status == 0 {
				return reply, wire, nil
			}
		}
	}
	return loco.Packet{}, nil, ErrBootstrap
}

func bookingTargets(body []byte) ([]string, []int, error) {
	raw := bson.Raw(body)
	ticket, err := raw.LookupErr("ticket")
	if err != nil || ticket.Type != bson.TypeEmbeddedDocument {
		return nil, nil, ErrProtocol
	}
	lsl, err := ticket.Document().LookupErr("lsl")
	if err != nil || lsl.Type != bson.TypeArray {
		return nil, nil, ErrProtocol
	}
	values, err := lsl.Array().Values()
	if err != nil {
		return nil, nil, ErrProtocol
	}
	var hosts []string
	for _, value := range values {
		if value.Type == bson.TypeString && value.StringValue() != "" {
			hosts = append(hosts, value.StringValue())
		}
	}
	wifi, err := raw.LookupErr("wifi")
	if err != nil || wifi.Type != bson.TypeEmbeddedDocument {
		return nil, nil, ErrProtocol
	}
	portValue, err := wifi.Document().LookupErr("ports")
	if err != nil || portValue.Type != bson.TypeArray {
		return nil, nil, ErrProtocol
	}
	values, err = portValue.Array().Values()
	if err != nil {
		return nil, nil, ErrProtocol
	}
	var ports []int
	for _, value := range values {
		if value.Type == bson.TypeInt32 {
			port := int(value.Int32())
			if port > 0 && port <= 65535 {
				ports = append(ports, port)
			}
		}
	}
	if len(hosts) == 0 {
		return nil, nil, ErrProtocol
	}
	if len(ports) == 0 {
		ports = []int{995}
	}
	return hosts, ports, nil
}

func endpoint(body []byte) (string, int, error) {
	raw := bson.Raw(body)
	hostValue, err := raw.LookupErr("host")
	if err != nil || hostValue.Type != bson.TypeString || hostValue.StringValue() == "" {
		return "", 0, ErrProtocol
	}
	portValue, err := raw.LookupErr("port")
	if err != nil {
		return "", 0, ErrProtocol
	}
	var port int
	switch portValue.Type {
	case bson.TypeInt32:
		port = int(portValue.Int32())
	case bson.TypeInt64:
		port = int(portValue.Int64())
	default:
		return "", 0, ErrProtocol
	}
	if port <= 0 || port > 65535 {
		return "", 0, ErrProtocol
	}
	return hostValue.StringValue(), port, nil
}

func finishLoginSync(wire *wireConn, first []byte, firstStatus int32, nextID uint32) ([]bson.Raw, uint32, []loco.Packet, loginCursor, error) {
	page := append(bson.Raw(nil), first...)
	status := firstStatus
	var chats []bson.Raw
	var pushes []loco.Packet
	var cursor loginCursor
	for range 20 {
		pageChats, eof, err := parseChatPageContent(page)
		if err != nil {
			return nil, nextID, pushes, cursor, err
		}
		// The official client applies per-chat deltas for its accepted negative
		// list statuses, but only a status-zero EOF commits global progress.
		if err := updateLoginCursor(page, &cursor, status == 0 && eof); err != nil {
			return nil, nextID, pushes, cursor, err
		}
		chats = append(chats, pageChats...)
		if status != 0 {
			if status == -305 || status == -310 {
				return chats, nextID, pushes, cursor, nil
			}
			return nil, nextID, pushes, cursor, ErrProtocol
		}
		if eof {
			return chats, nextID, pushes, cursor, nil
		}
		lastTokenID, err := bsonInt64(page, "lastTokenId")
		if err != nil {
			return nil, nextID, pushes, cursor, ErrProtocol
		}
		lastChatID, err := bsonInt64(page, "lastChatId")
		if err != nil {
			return nil, nextID, pushes, cursor, ErrProtocol
		}
		body, err := bson.Marshal(bson.D{
			{Key: "lastTokenId", Value: lastTokenID},
			{Key: "lastChatId", Value: lastChatID},
		})
		if err != nil {
			return nil, nextID, pushes, cursor, ErrProtocol
		}
		reply, unsolicited, err := wire.request(nextID, "LCHATLIST", body)
		nextID++
		pushes = append(pushes, unsolicited...)
		replyStatus, statusErr := responseStatus(reply)
		if err != nil || statusErr != nil || (replyStatus != 0 && replyStatus != -310) {
			return nil, nextID, pushes, cursor, ErrProtocol
		}
		status = replyStatus
		page = append(page[:0], reply.Body...)
	}
	return nil, nextID, pushes, cursor, ErrProtocol
}

func updateLoginCursor(page bson.Raw, cursor *loginCursor, updateGlobal bool) error {
	if updateGlobal {
		if value, err := bsonInt64(page, "lastTokenId"); err == nil && value >= 0 {
			copy := value
			cursor.lastTokenID = &copy
		}
		if value, err := bsonInt64(page, "lbk"); err == nil && value >= 0 && value <= 1<<31-1 {
			copy := int32(value)
			cursor.lbk = &copy
		}
	}
	if value, err := page.LookupErr("chatDatas"); err == nil {
		if value.Type != bson.TypeArray {
			return ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return ErrProtocol
		}
		for _, value := range values {
			if value.Type != bson.TypeEmbeddedDocument {
				return ErrProtocol
			}
			target, err := loginChatTarget(value.Document())
			if err != nil {
				return err
			}
			setLoginTarget(&cursor.observed, target)
		}
	}
	if value, err := page.LookupErr("delChatIds"); err == nil {
		if value.Type != bson.TypeArray {
			return ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return ErrProtocol
		}
		for _, value := range values {
			var chatID int64
			switch value.Type {
			case bson.TypeInt32:
				chatID = int64(value.Int32())
			case bson.TypeInt64:
				chatID = value.Int64()
			default:
				return ErrProtocol
			}
			if chatID <= 0 {
				return ErrProtocol
			}
			cursor.deleted = append(cursor.deleted, chatID)
		}
	}
	return nil
}

func loginChatTarget(raw bson.Raw) (continuity.ChatTarget, error) {
	chatID, err := bsonInt64(raw, "c")
	if err != nil || chatID <= 0 {
		return continuity.ChatTarget{}, ErrProtocol
	}
	target := continuity.ChatTarget{ChatID: chatID}
	last, err := raw.LookupErr("l")
	if err != nil || last.Type == bson.TypeNull {
		return target, nil
	}
	if last.Type != bson.TypeEmbeddedDocument {
		return continuity.ChatTarget{}, ErrProtocol
	}
	logID, err := bsonInt64(last.Document(), "logId")
	if err != nil || logID <= 0 {
		return continuity.ChatTarget{}, ErrProtocol
	}
	if nestedChatID, nestedErr := bsonInt64(last.Document(), "chatId"); nestedErr == nil && nestedChatID != chatID {
		return continuity.ChatTarget{}, ErrProtocol
	}
	target.MaxLogID = logID
	return target, nil
}

func setLoginTarget(targets *[]continuity.ChatTarget, target continuity.ChatTarget) {
	for i := range *targets {
		if (*targets)[i].ChatID == target.ChatID {
			(*targets)[i] = target
			return
		}
	}
	*targets = append(*targets, target)
}

func parseChatPage(page bson.Raw) ([]bson.Raw, bool, int64, int64, error) {
	chats, eof, err := parseChatPageContent(page)
	if err != nil {
		return nil, false, 0, 0, err
	}
	if eof {
		return chats, true, 0, 0, nil
	}
	lastTokenID, err := bsonInt64(page, "lastTokenId")
	if err != nil {
		return nil, false, 0, 0, ErrProtocol
	}
	lastChatID, err := bsonInt64(page, "lastChatId")
	if err != nil {
		return nil, false, 0, 0, ErrProtocol
	}
	return chats, false, lastTokenID, lastChatID, nil
}

func parseChatPageContent(page bson.Raw) ([]bson.Raw, bool, error) {
	if err := page.Validate(); err != nil {
		return nil, false, ErrProtocol
	}
	var chats []bson.Raw
	if value, err := page.LookupErr("chatDatas"); err == nil {
		if value.Type != bson.TypeArray {
			return nil, false, ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return nil, false, ErrProtocol
		}
		for _, value := range values {
			if value.Type != bson.TypeEmbeddedDocument {
				return nil, false, ErrProtocol
			}
			chats = append(chats, append(bson.Raw(nil), value.Document()...))
		}
	}
	eofValue, err := page.LookupErr("eof")
	if err != nil || eofValue.Type != bson.TypeBoolean {
		return nil, false, ErrProtocol
	}
	return chats, eofValue.Boolean(), nil
}

func bsonInt64(raw bson.Raw, key string) (int64, error) {
	value, err := raw.LookupErr(key)
	if err != nil {
		return 0, err
	}
	switch value.Type {
	case bson.TypeInt32:
		return int64(value.Int32()), nil
	case bson.TypeInt64:
		return value.Int64(), nil
	default:
		return 0, ErrProtocol
	}
}

func responseStatus(packet loco.Packet) (int32, error) {
	value, err := bson.Raw(packet.Body).LookupErr("status")
	if err != nil {
		return 0, ErrProtocol
	}
	var status int64
	switch value.Type {
	case bson.TypeInt32:
		status = int64(value.Int32())
	case bson.TypeInt64:
		status = value.Int64()
	default:
		return 0, ErrProtocol
	}
	if status < -1<<31 || status > 1<<31-1 {
		return 0, ErrProtocol
	}
	return int32(status), nil
}
