package client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

type headerReadResult struct {
	packet loco.Packet
	err    error
}

func awaitHeaderReadResult(t *testing.T, results <-chan headerReadResult) headerReadResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("wire read did not finish")
		return headerReadResult{}
	}
}

func awaitHeaderReadError(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(time.Second):
		t.Fatal("wire read did not finish")
		return nil
	}
}

func TestWireReadHeaderObserverPlainRunsBeforeBody(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	packet, err := (loco.Packet{Header: loco.Header{PacketID: 7, Method: "PING"}, Body: []byte("body")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	headerSeen := make(chan loco.Header, 1)
	result := make(chan headerReadResult, 1)
	go func() {
		got, readErr := wire.readWithHeaderObserver(func(header loco.Header) { headerSeen <- header })
		result <- headerReadResult{got, readErr}
	}()
	if _, err := serverConn.Write(packet[:loco.HeaderSize-1]); err != nil {
		t.Fatal(err)
	}
	select {
	case header := <-headerSeen:
		t.Fatalf("header observed before complete header: %#v", header)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := serverConn.Write(packet[loco.HeaderSize-1 : loco.HeaderSize]); err != nil {
		t.Fatal(err)
	}
	select {
	case header := <-headerSeen:
		if header.PacketID != 7 || header.Method != "PING" || header.BodyLen != 4 {
			t.Fatalf("unexpected observed header: %#v", header)
		}
	case <-time.After(time.Second):
		t.Fatal("header observer did not run before body release")
	}
	if _, err := serverConn.Write(packet[loco.HeaderSize:]); err != nil {
		t.Fatal(err)
	}
	got := awaitHeaderReadResult(t, result)
	if got.err != nil || string(got.packet.Body) != "body" {
		t.Fatalf("read result=%#v", got)
	}
}

func TestWireReadHeaderObserverPlainMalformedHeaderIsSilent(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	header := bytes.Repeat([]byte{0}, loco.HeaderSize)
	seen := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		_, err := wire.readWithHeaderObserver(func(loco.Header) { seen <- struct{}{} })
		result <- err
	}()
	if _, err := serverConn.Write(header); err != nil {
		t.Fatal(err)
	}
	if err := awaitHeaderReadError(t, result); !errors.Is(err, loco.ErrInvalidMethod) {
		t.Fatalf("read error=%v, want invalid method", err)
	}
	select {
	case <-seen:
		t.Fatal("observer ran for malformed header")
	default:
	}
}

func TestWireReadHeaderObserverPlainBodyEOFRunsOnce(t *testing.T) {
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
	seen := make(chan loco.Header, 2)
	result := make(chan error, 1)
	go func() {
		_, readErr := wire.readWithHeaderObserver(func(header loco.Header) { seen <- header })
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
	if err := awaitHeaderReadError(t, result); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error=%v, want unexpected EOF", err)
	}
	select {
	case observed := <-seen:
		if observed.PacketID != 8 {
			t.Fatalf("observed header=%#v", observed)
		}
	default:
		t.Fatal("observer did not run before body EOF")
	}
	select {
	case <-seen:
		t.Fatal("observer ran more than once")
	default:
	}
}

func TestWireReadHeaderObserverSequentialReadsOneCallbackEach(t *testing.T) {
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
	seen := make(chan loco.Header, 2)
	got, err := wire.readWithHeaderObserver(func(header loco.Header) { seen <- header })
	if err != nil || got.Header.PacketID != 11 || string(got.Body) != "a" {
		t.Fatalf("first read=%#v err=%v", got, err)
	}
	got, err = wire.readWithHeaderObserver(nil)
	if err != nil || got.Header.PacketID != 12 || string(got.Body) != "b" {
		t.Fatalf("second read=%#v err=%v", got, err)
	}
	select {
	case header := <-seen:
		if header.PacketID != 11 {
			t.Fatalf("observed header=%#v", header)
		}
	case <-time.After(time.Second):
		t.Fatal("first observer callback missing")
	}
	select {
	case header := <-seen:
		t.Fatalf("nil observer unexpectedly produced callback: %#v", header)
	default:
	}
	if err := awaitHeaderReadError(t, serverDone); err != nil {
		t.Fatal(err)
	}
}

func TestWireReadHeaderObserverSecureRunsAfterAuthenticatedEnvelope(t *testing.T) {
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
	seen := make(chan loco.Header, 1)
	result := make(chan error, 1)
	go func() {
		got, readErr := wire.readWithHeaderObserver(func(header loco.Header) { seen <- header })
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
	select {
	case header := <-seen:
		if header.PacketID != 9 || header.Method != "PUSH" {
			t.Fatalf("unexpected secure header: %#v", header)
		}
	case <-time.After(time.Second):
		t.Fatal("secure header observer did not run")
	}
	if err := awaitHeaderReadError(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestWireReadHeaderObserverSecureAuthFailureIsSilent(t *testing.T) {
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
	seen := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		_, readErr := wire.readWithHeaderObserver(func(loco.Header) { seen <- struct{}{} })
		result <- readErr
	}()
	if _, err := io.Copy(serverConn, bytes.NewReader(envelope)); err != nil {
		t.Fatal(err)
	}
	if err := awaitHeaderReadError(t, result); !errors.Is(err, loco.ErrInvalidSecureEnvelope) {
		t.Fatalf("read error=%v, want secure auth failure", err)
	}
	select {
	case <-seen:
		t.Fatal("observer ran after secure authentication failure")
	default:
	}
}

func TestWireReadHeaderObserverSecureCoalescedEnvelopePreservesOrder(t *testing.T) {
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
	var events []string
	got, err := wire.readWithHeaderObserver(func(header loco.Header) { events = append(events, "H"+header.Method) })
	if err == nil {
		events = append(events, "P"+got.Header.Method)
	}
	if err != nil || got.Header.PacketID != 7 {
		t.Fatalf("first packet=%#v err=%v", got, err)
	}
	got, err = wire.readWithHeaderObserver(func(header loco.Header) { events = append(events, "H"+header.Method) })
	if err == nil {
		events = append(events, "P"+got.Header.Method)
	}
	if err != nil || got.Header.PacketID != 8 {
		t.Fatalf("second packet=%#v err=%v", got, err)
	}
	if want := []string{"HONE", "PONE", "HTWO", "PTWO"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
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

func TestWireReadHeaderObserverSecurePacketSplitAcrossEnvelopes(t *testing.T) {
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
			seen := 0
			got, err := wire.readWithHeaderObserver(func(header loco.Header) {
				seen++
				if header.PacketID != 17 {
					t.Errorf("header=%#v", header)
				}
			})
			if err != nil || got.Header.PacketID != 17 || string(got.Body) != "payload" || seen != 1 {
				t.Fatalf("packet=%#v err=%v headers=%d", got, err, seen)
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

func TestWireReadHeaderObserverSecureAuthFailureAfterPartialPlaintextIsSilent(t *testing.T) {
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
	seen := 0
	_, err = wire.readWithHeaderObserver(func(loco.Header) { seen++ })
	if !errors.Is(err, loco.ErrInvalidSecureEnvelope) || seen != 0 {
		t.Fatalf("err=%v headers=%d", err, seen)
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

func TestWireReadHeaderObserverSecureZeroPrefixContinuesHeaderRead(t *testing.T) {
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
	seen := 0
	packet, err := wire.readWithHeaderObserver(func(header loco.Header) {
		seen++
		if header.PacketID != 23 {
			t.Errorf("header=%#v", header)
		}
	})
	if err != nil || packet.Header.PacketID != 23 || string(packet.Body) != "ok" || seen != 1 {
		t.Fatalf("packet=%#v err=%v headers=%d", packet, err, seen)
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

func TestWireReadHeaderObserverSecurePartialPlaintextEOF(t *testing.T) {
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
			seen := 0
			_, readErr := wire.readWithHeaderObserver(func(loco.Header) { seen++ })
			if !errors.Is(readErr, io.ErrUnexpectedEOF) {
				t.Fatalf("read error=%v", readErr)
			}
			wantHeaders := 0
			if cut >= loco.HeaderSize {
				wantHeaders = 1
			}
			if seen != wantHeaders {
				t.Fatalf("headers=%d want=%d", seen, wantHeaders)
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
