package client

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// wireConn is one framed LOCO connection: a TLS booking or checkin
// connection, or a secure carriage or media connection.
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
