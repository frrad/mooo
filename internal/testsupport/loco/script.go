// Package loco provides a small in-memory scripted LOCO peer for integration
// tests. It deliberately knows only packet framing and the synthetic SecureV3
// transport; client bootstrap policy remains in internal/client.
package loco

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	protocol "github.com/frrad/mooo/internal/protocol/loco"
)

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
		clientSecure, err = protocol.NewSecureV3(nil, 0)
		if err != nil {
			_ = clientConn.Close()
			_ = serverConn.Close()
			return nil, err
		}
		serverSecure, err = protocol.NewSecureV3WithKey(clientSecure.KeyForTesting(), nil, 0)
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
