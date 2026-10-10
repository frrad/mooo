// Package loco provides the in-memory scripted LOCO peer that client and
// connector tests share. It knows only packet framing, BSON request/reply
// steps and the synthetic SecureV3 transport; client bootstrap policy remains
// in internal/client.
package loco

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	protocol "github.com/frrad/mooo/internal/protocol/loco"
)

// NewKeyedSecureV3 builds a SecureV3 that uses key as its session key, so two
// test peers can share one synthetic key. It feeds key to protocol.NewSecureV3
// as the first bytes of its random source; random (crypto/rand when nil)
// supplies the nonces after that.
func NewKeyedSecureV3(key []byte, random io.Reader, maxCiphertext uint32) (*protocol.SecureV3, error) {
	if len(key) != protocol.V3KeySize {
		return nil, protocol.ErrInvalidKey
	}
	if random == nil {
		random = rand.Reader
	}
	return protocol.NewSecureV3(io.MultiReader(bytes.NewReader(bytes.Clone(key)), random), maxCiphertext)
}

type Endpoint struct {
	Client       net.Conn
	ClientSecure *protocol.SecureV3
	Server       *Peer
}

type Peer struct {
	Conn   net.Conn
	Secure *protocol.SecureV3
}

type Step func(*Peer) error

type Backend struct {
	Endpoint Endpoint
	done     chan error
}

func NewBackend(secure bool, steps ...Step) (*Backend, error) {
	clientConn, serverConn := net.Pipe()
	var clientSecure, serverSecure *protocol.SecureV3
	var err error
	if secure {
		key := make([]byte, protocol.V3KeySize)
		if _, err = rand.Read(key); err == nil {
			clientSecure, err = NewKeyedSecureV3(key, nil, 0)
		}
		if err == nil {
			serverSecure, err = NewKeyedSecureV3(key, nil, 0)
		}
		if err != nil {
			_ = clientConn.Close()
			_ = serverConn.Close()
			return nil, err
		}
	}
	b := &Backend{
		Endpoint: Endpoint{Client: clientConn, ClientSecure: clientSecure, Server: &Peer{Conn: serverConn, Secure: serverSecure}},
		done:     make(chan error, 1),
	}
	go func() {
		defer func() { _ = serverConn.Close() }()
		for i, step := range steps {
			if err := step(b.Endpoint.Server); err != nil {
				b.done <- fmt.Errorf("script step %d: %w", i+1, err)
				return
			}
		}
		b.done <- nil
	}()
	return b, nil
}

func (b *Backend) Wait(timeout time.Duration) error {
	select {
	case err := <-b.done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("scripted backend did not finish")
	}
}

func (p *Peer) Read() (protocol.Packet, error) {
	raw, err := readFrame(p.Conn, p.Secure != nil)
	if err != nil {
		return protocol.Packet{}, err
	}
	if p.Secure != nil {
		raw, err = p.Secure.Decrypt(raw)
		if err != nil {
			return protocol.Packet{}, err
		}
	}
	return parsePacket(raw)
}

func (p *Peer) Write(packet protocol.Packet) error {
	raw, err := packet.MarshalBinary(0)
	if err != nil {
		return err
	}
	if p.Secure != nil {
		raw, err = p.Secure.Encrypt(raw)
		if err != nil {
			return err
		}
	}
	return writeAll(p.Conn, raw)
}

// WriteBSON writes one BSON-bodied packet. id is the request's packet ID for
// a reply and zero for a push. body is a raw []byte or anything bson.Marshal
// accepts.
func (p *Peer) WriteBSON(id uint32, method string, body any) error {
	raw, ok := body.([]byte)
	if !ok {
		var err error
		if raw, err = bson.Marshal(body); err != nil {
			return err
		}
	}
	return p.Write(protocol.Packet{Header: protocol.Header{PacketID: id, Method: method, BodyType: protocol.BodyTypeBSON}, Body: raw})
}

// ReadPayload reads one secure frame and returns its decrypted bytes without
// parsing them as a packet, as a media peer reads an upload stream.
func (p *Peer) ReadPayload() ([]byte, error) {
	if p.Secure == nil {
		return nil, errors.New("ReadPayload needs a secure peer")
	}
	raw, err := readFrame(p.Conn, true)
	if err != nil {
		return nil, err
	}
	return p.Secure.Decrypt(raw)
}

// Expect reads one request, requires its method, passes its BSON body to
// check when check is not nil, and answers with reply under the request's
// packet ID.
func Expect(method string, check func(bson.Raw) error, reply bson.D) Step {
	return func(peer *Peer) error {
		request, err := peer.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != method {
			return fmt.Errorf("method = %q, want %q", request.Header.Method, method)
		}
		body := bson.Raw(request.Body)
		if err := body.Validate(); err != nil {
			return fmt.Errorf("invalid request BSON: %w", err)
		}
		if check != nil {
			if err := check(body); err != nil {
				return err
			}
		}
		return peer.WriteBSON(request.Header.PacketID, method, reply)
	}
}

// PushAfter writes an unsolicited packet once trigger is closed.
func PushAfter(trigger <-chan struct{}, method string, body bson.D) Step {
	return func(peer *Peer) error {
		select {
		case <-trigger:
		case <-time.After(2 * time.Second):
			return errors.New("push trigger timeout")
		}
		return peer.WriteBSON(0, method, body)
	}
}

// DropAfter reads one request for method, counts it, and closes the
// connection without answering.
func DropAfter(method string, count *int) Step {
	return func(peer *Peer) error {
		request, err := peer.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != method {
			return fmt.Errorf("method = %q, want %q", request.Header.Method, method)
		}
		*count++
		return peer.Conn.Close()
	}
}

// HoldOpen keeps the connection open until the client closes it, and fails if
// the client sends a packet first.
func HoldOpen() Step {
	return func(peer *Peer) error {
		packet, err := peer.Read()
		if err != nil {
			return nil
		}
		return fmt.Errorf("unexpected packet %s while holding backend open", packet.Header.Method)
	}
}

// Hosts routes dials to the scripted backend registered for each host.
type Hosts map[string]*Backend

// Dial returns the client end of host's backend. Its signature matches
// client.TestDialer.
func (h Hosts) Dial(_ context.Context, host string, _ int) (net.Conn, *protocol.SecureV3, error) {
	backend, ok := h[host]
	if !ok {
		return nil, nil, fmt.Errorf("unexpected host %q", host)
	}
	return backend.Endpoint.Client, backend.Endpoint.ClientSecure, nil
}

func readFrame(conn net.Conn, secure bool) ([]byte, error) {
	if secure {
		prefix := make([]byte, 4)
		if _, err := io.ReadFull(conn, prefix); err != nil {
			return nil, err
		}
		n := binary.LittleEndian.Uint32(prefix)
		if n > protocol.DefaultMaxCiphertext {
			return nil, protocol.ErrCiphertextTooLarge
		}
		frame := make([]byte, 4+int(n))
		copy(frame, prefix)
		_, err := io.ReadFull(conn, frame[4:])
		return frame, err
	}
	header := make([]byte, protocol.HeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	h, err := protocol.ParseHeader(header, protocol.DefaultMaxBody)
	if err != nil {
		return nil, err
	}
	body := make([]byte, h.BodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return append(header, body...), nil
}

func parsePacket(raw []byte) (protocol.Packet, error) {
	if len(raw) < protocol.HeaderSize {
		return protocol.Packet{}, io.ErrUnexpectedEOF
	}
	h, err := protocol.ParseHeader(raw[:protocol.HeaderSize], protocol.DefaultMaxBody)
	if err != nil {
		return protocol.Packet{}, err
	}
	if len(raw) != protocol.HeaderSize+int(h.BodyLen) {
		return protocol.Packet{}, io.ErrUnexpectedEOF
	}
	return protocol.Packet{Header: h, Body: append([]byte(nil), raw[protocol.HeaderSize:]...)}, nil
}

func writeAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
