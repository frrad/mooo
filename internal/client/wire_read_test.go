package client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func awaitWireReadError(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(time.Second):
		t.Fatal("wire read did not finish")
		return nil
	}
}

func TestWireReadPlainMalformedHeader(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	header := bytes.Repeat([]byte{0}, loco.HeaderSize)
	result := make(chan error, 1)
	go func() {
		_, err := wire.read()
		result <- err
	}()
	if _, err := serverConn.Write(header); err != nil {
		t.Fatal(err)
	}
	if err := awaitWireReadError(t, result); !errors.Is(err, loco.ErrInvalidMethod) {
		t.Fatalf("read error=%v, want invalid method", err)
	}
}

func TestWireReadPlainBodyEOF(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	header, err := (loco.Header{PacketID: 8, Method: "PUSH", BodyLen: 4}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, readErr := wire.read()
		result <- readErr
	}()
	if _, err := serverConn.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := awaitWireReadError(t, result); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error=%v, want unexpected EOF", err)
	}
}

func TestWireReadPlainCoalescedFramesReadSequentially(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	first, err := (loco.Packet{Header: loco.Header{PacketID: 11, Method: "ONE"}, Body: []byte("a")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (loco.Packet{Header: loco.Header{PacketID: 12, Method: "TWO"}, Body: []byte("b")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		_, writeErr := serverConn.Write(append(first, second...))
		serverDone <- writeErr
	}()
	got, err := wire.read()
	if err != nil || got.Header.PacketID != 11 || string(got.Body) != "a" {
		t.Fatalf("first read=%#v err=%v", got, err)
	}
	got, err = wire.read()
	if err != nil || got.Header.PacketID != 12 || string(got.Body) != "b" {
		t.Fatalf("second read=%#v err=%v", got, err)
	}
	if err := awaitWireReadError(t, serverDone); err != nil {
		t.Fatal(err)
	}
}

func TestWireReadSecureReassemblesChunkedEnvelope(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	key := bytes.Repeat([]byte{0x42}, loco.V3KeySize)
	clientSecure, err := loco.NewSecureV3WithKey(key, bytes.NewReader(bytes.Repeat([]byte{1}, 64)), 0)
	if err != nil {
		t.Fatal(err)
	}
	serverSecure, err := loco.NewSecureV3WithKey(key, bytes.NewReader(bytes.Repeat([]byte{2}, 64)), 0)
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: clientConn, secure: clientSecure}
	plain, err := (loco.Packet{Header: loco.Header{PacketID: 9, Method: "PUSH"}, Body: []byte("secure")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := serverSecure.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		got, readErr := wire.read()
		if readErr == nil && (got.Header.PacketID != 9 || string(got.Body) != "secure") {
			readErr = errors.New("unexpected secure packet")
		}
		result <- readErr
	}()
	for start := 0; start < len(envelope); start += 3 {
		end := start + 3
		if end > len(envelope) {
			end = len(envelope)
		}
		if _, err := serverConn.Write(envelope[start:end]); err != nil {
			t.Fatal(err)
		}
	}
	if err := awaitWireReadError(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestWireReadSecureAuthFailure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	key := bytes.Repeat([]byte{0x42}, loco.V3KeySize)
	clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: clientConn, secure: clientSecure}
	plain, err := (loco.Packet{Header: loco.Header{PacketID: 10, Method: "PUSH"}}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := serverSecure.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	envelope[len(envelope)-1] ^= 1
	result := make(chan error, 1)
	go func() {
		_, readErr := wire.read()
		result <- readErr
	}()
	if _, err := io.Copy(serverConn, bytes.NewReader(envelope)); err != nil {
		t.Fatal(err)
	}
	if err := awaitWireReadError(t, result); !errors.Is(err, loco.ErrInvalidSecureEnvelope) {
		t.Fatalf("read error=%v, want secure auth failure", err)
	}
}

func TestWireReadSecureCoalescedEnvelopePreservesOrder(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	key := bytes.Repeat([]byte{0x51}, loco.V3KeySize)
	clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: clientConn, secure: clientSecure}
	first, err := (loco.Packet{Header: loco.Header{PacketID: 7, Method: "ONE"}, Body: []byte("a")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (loco.Packet{Header: loco.Header{PacketID: 8, Method: "TWO"}, Body: []byte("b")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := serverSecure.Encrypt(append(first, second...))
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		_, writeErr := serverConn.Write(envelope)
		serverDone <- writeErr
	}()
	got, err := wire.read()
	if err != nil || got.Header.PacketID != 7 || got.Header.Method != "ONE" {
		t.Fatalf("first packet=%#v err=%v", got, err)
	}
	got, err = wire.read()
	if err != nil || got.Header.PacketID != 8 || got.Header.Method != "TWO" {
		t.Fatalf("second packet=%#v err=%v", got, err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server write did not finish")
	}
}

func TestWireReadSecurePacketSplitAcrossEnvelopes(t *testing.T) {
	for _, cut := range []int{10, loco.HeaderSize + 2} {
		t.Run(fmt.Sprintf("cut-%d", cut), func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			_ = clientConn.SetDeadline(time.Now().Add(time.Second))
			_ = serverConn.SetDeadline(time.Now().Add(time.Second))
			defer func() { _ = clientConn.Close() }()
			defer func() { _ = serverConn.Close() }()
			key := bytes.Repeat([]byte{0x61}, loco.V3KeySize)
			clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			wire := &wireConn{c: clientConn, secure: clientSecure}
			frame, err := (loco.Packet{Header: loco.Header{PacketID: 17, Method: "SPLIT"}, Body: []byte("payload")}).MarshalBinary(64)
			if err != nil {
				t.Fatal(err)
			}
			first, err := serverSecure.Encrypt(frame[:cut])
			if err != nil {
				t.Fatal(err)
			}
			second, err := serverSecure.Encrypt(frame[cut:])
			if err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() {
				if _, writeErr := serverConn.Write(first); writeErr != nil {
					serverDone <- writeErr
					return
				}
				_, writeErr := serverConn.Write(second)
				serverDone <- writeErr
			}()
			got, err := wire.read()
			if err != nil || got.Header.PacketID != 17 || string(got.Body) != "payload" {
				t.Fatalf("packet=%#v err=%v", got, err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("server writes did not finish")
			}
		})
	}
}

func TestWireReadSecureAuthFailureAfterPartialPlaintext(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	key := bytes.Repeat([]byte{0x71}, loco.V3KeySize)
	clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: clientConn, secure: clientSecure}
	frame, err := (loco.Packet{Header: loco.Header{PacketID: 19, Method: "AUTH"}, Body: []byte("payload")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	first, err := serverSecure.Encrypt(frame[:10])
	if err != nil {
		t.Fatal(err)
	}
	second, err := serverSecure.Encrypt(frame[10:])
	if err != nil {
		t.Fatal(err)
	}
	second[len(second)-1] ^= 1
	serverDone := make(chan error, 1)
	go func() {
		if _, writeErr := serverConn.Write(first); writeErr != nil {
			serverDone <- writeErr
			return
		}
		_, writeErr := serverConn.Write(second)
		serverDone <- writeErr
	}()
	_, err = wire.read()
	if !errors.Is(err, loco.ErrInvalidSecureEnvelope) {
		t.Fatalf("err=%v", err)
	}
	select {
	case writeErr := <-serverDone:
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server writes did not finish")
	}
}

func TestWireReadSecureZeroPrefixIsSkipped(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	key := bytes.Repeat([]byte{0x81}, loco.V3KeySize)
	clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: clientConn, secure: clientSecure}
	frame, err := (loco.Packet{Header: loco.Header{PacketID: 23, Method: "ZERO"}, Body: []byte("ok")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := serverSecure.Encrypt(frame)
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		var zero [4]byte
		if _, writeErr := serverConn.Write(zero[:]); writeErr != nil {
			serverDone <- writeErr
			return
		}
		_, writeErr := serverConn.Write(envelope)
		serverDone <- writeErr
	}()
	packet, err := wire.read()
	if err != nil || packet.Header.PacketID != 23 || string(packet.Body) != "ok" {
		t.Fatalf("packet=%#v err=%v", packet, err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server writer did not finish")
	}
}

func TestWireReadSecurePartialPlaintextEOF(t *testing.T) {
	for _, cut := range []int{10, loco.HeaderSize + 2} {
		t.Run(fmt.Sprintf("cut-%d", cut), func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			_ = clientConn.SetDeadline(time.Now().Add(time.Second))
			_ = serverConn.SetDeadline(time.Now().Add(time.Second))
			defer func() { _ = clientConn.Close() }()
			defer func() { _ = serverConn.Close() }()
			key := bytes.Repeat([]byte{0xA1}, loco.V3KeySize)
			clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			wire := &wireConn{c: clientConn, secure: clientSecure}
			frame, err := (loco.Packet{Header: loco.Header{PacketID: 29, Method: "EOF"}, Body: []byte("payload")}).MarshalBinary(64)
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := serverSecure.Encrypt(frame[:cut])
			if err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() {
				_, writeErr := serverConn.Write(envelope)
				_ = serverConn.Close()
				serverDone <- writeErr
			}()
			_, readErr := wire.read()
			if !errors.Is(readErr, io.ErrUnexpectedEOF) {
				t.Fatalf("read error=%v", readErr)
			}
			select {
			case writeErr := <-serverDone:
				if writeErr != nil {
					t.Fatal(writeErr)
				}
			case <-time.After(time.Second):
				t.Fatal("server write did not finish")
			}
		})
	}
}
