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
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	bookingHost  = "booking-loco.kakao.com"
	requestLimit = 64
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

// Session serializes requests over one authenticated carriage. Unsolicited
// packets are preserved for the caller while a response is being awaited.
type Session struct {
	mu              sync.Mutex
	wire            *wireConn
	nextID          uint32
	closed          bool
	pushes          chan loco.Packet
	initialChatData []bson.Raw
}

// connectSession performs GETCONF, CHECKIN, secure carriage setup, and
// LOGINLIST from client-owned state. Client owns and reuses the result.
func connectSession(ctx context.Context, state authstate.State) (*Session, error) {
	if ctx == nil || state.Credentials == nil {
		return nil, ErrCredentialsAbsent
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
	booking, err := dialTLS(ctx, bookingHost, 443)
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
	checkinReply, _, err := checkin(ctx, hosts, ports, checkinBody)
	if err != nil {
		return nil, ErrBootstrap
	}
	carriageHost, carriagePort, err := endpoint(checkinReply.Body)
	if err != nil {
		return nil, ErrBootstrap
	}
	carriage, err := dialSecure(ctx, carriageHost, carriagePort)
	if err != nil {
		return nil, ErrBootstrap
	}

	loginBody, err := (sessionlogin.LoginListRequest{
		AppVer: state.Identity.Metadata.AppVersion, OS: "mac", Lang: "en", DUUID: wireUUID,
		OAuthToken: state.Credentials.AccessToken, NType: 0, MCCMNC: "99999", Revision: 0,
		DType: 2, PCST: 0, RP: []byte{0, 0, 0xff, 0xff, 0, 0}, BG: false,
		ChatIDs: []int64{}, MaxIDs: []int64{}, LastTokenID: 0, LBK: 0,
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
	chatData, nextID, pendingPushes, err := finishLoginSync(carriage, loginReply.Body, 3)
	if err != nil {
		_ = carriage.close()
		return nil, ErrLogin
	}
	session := &Session{
		wire:            carriage,
		nextID:          nextID,
		pushes:          make(chan loco.Packet, requestLimit),
		initialChatData: chatData,
	}
	for _, packet := range pendingPushes {
		session.pushes <- packet
	}
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

// Pushes exposes unsolicited packets observed while Request waits for its
// correlated response. Callers should drain it continuously once mutations or
// subscriptions that can produce events are active.
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
	defer s.mu.Unlock()
	if s.closed || s.wire == nil {
		return loco.Packet{}, ErrClosed
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.wire.c.SetDeadline(deadline)
	} else {
		_ = s.wire.c.SetDeadline(time.Now().Add(15 * time.Second))
	}
	id := s.nextID
	s.nextID++
	reply, unsolicited, err := s.wire.request(id, command, body)
	for _, packet := range unsolicited {
		select {
		case s.pushes <- packet:
		default:
			return loco.Packet{}, ErrProtocol
		}
	}
	if err != nil {
		return loco.Packet{}, fmt.Errorf("client: %s request: %w", command, err)
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

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.pushes != nil {
		close(s.pushes)
	}
	if s.wire == nil {
		return nil
	}
	return s.wire.close()
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

func checkin(ctx context.Context, hosts []string, ports []int, body []byte) (loco.Packet, *wireConn, error) {
	for _, host := range hosts {
		for _, port := range append([]int{443}, ports...) {
			wire, err := dialTLS(ctx, host, port)
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
			wire, err := dialSecure(ctx, host, port)
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

func finishLoginSync(wire *wireConn, first []byte, nextID uint32) ([]bson.Raw, uint32, []loco.Packet, error) {
	page := append(bson.Raw(nil), first...)
	var chats []bson.Raw
	var pushes []loco.Packet
	for range 20 {
		pageChats, eof, lastTokenID, lastChatID, err := parseChatPage(page)
		if err != nil {
			return nil, nextID, pushes, err
		}
		chats = append(chats, pageChats...)
		if eof {
			return chats, nextID, pushes, nil
		}
		body, err := bson.Marshal(bson.D{
			{Key: "lastTokenId", Value: lastTokenID},
			{Key: "lastChatId", Value: lastChatID},
		})
		if err != nil {
			return nil, nextID, pushes, ErrProtocol
		}
		reply, unsolicited, err := wire.request(nextID, "LCHATLIST", body)
		nextID++
		pushes = append(pushes, unsolicited...)
		status, statusErr := responseStatus(reply)
		if err != nil || statusErr != nil || status != 0 {
			return nil, nextID, pushes, ErrProtocol
		}
		page = append(page[:0], reply.Body...)
	}
	return nil, nextID, pushes, ErrProtocol
}

func parseChatPage(page bson.Raw) ([]bson.Raw, bool, int64, int64, error) {
	if err := page.Validate(); err != nil {
		return nil, false, 0, 0, ErrProtocol
	}
	var chats []bson.Raw
	if value, err := page.LookupErr("chatDatas"); err == nil {
		if value.Type != bson.TypeArray {
			return nil, false, 0, 0, ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return nil, false, 0, 0, ErrProtocol
		}
		for _, value := range values {
			if value.Type != bson.TypeEmbeddedDocument {
				return nil, false, 0, 0, ErrProtocol
			}
			chats = append(chats, append(bson.Raw(nil), value.Document()...))
		}
	}
	eofValue, err := page.LookupErr("eof")
	if err != nil || eofValue.Type != bson.TypeBoolean {
		return nil, false, 0, 0, ErrProtocol
	}
	if eofValue.Boolean() {
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
