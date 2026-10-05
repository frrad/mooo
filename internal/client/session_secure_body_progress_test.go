package client

import (
	"bytes"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func secureEnvelopeFixture(t *testing.T) ([]byte, *loco.SecureV3) {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, loco.V3KeySize)
	decryptor, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := (loco.Packet{Header: loco.Header{PacketID: 9, Method: "PUSH"}, Body: []byte("secure")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptor.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted, decryptor
}

func TestSessionSecureEnvelopeBodyWatchdogUsesCiphertextBody(t *testing.T) {
	encrypted, decryptor := secureEnvelopeFixture(t)
	owner := &bodyProgressOwnerSpy{}
	session := newSession(nil)
	if err := session.BindInSegmentTimeout(owner); err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: &chunkConn{Reader: bytes.NewReader(encrypted), chunks: []int{4, 1, len(encrypted)}}, secure: decryptor}
	if _, err := wire.readWithHeaderObserverAndProgress(nil, session.bodyProgressCallbacks()); err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 0, 1, 0}
	if !bytes.Equal(owner.toggles, want) {
		t.Fatalf("secure body transitions=%v want %v", owner.toggles, want)
	}
}

func TestSessionSecureZeroEnvelopeDoesNotArmWatchdog(t *testing.T) {
	encrypted, decryptor := secureEnvelopeFixture(t)
	input := append([]byte{0, 0, 0, 0}, encrypted...)
	owner := &bodyProgressOwnerSpy{}
	session := newSession(nil)
	if err := session.BindInSegmentTimeout(owner); err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: &chunkConn{Reader: bytes.NewReader(input), chunks: []int{4, 4, 1, len(encrypted)}}, secure: decryptor}
	if _, err := wire.readWithHeaderObserverAndProgress(nil, session.bodyProgressCallbacks()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(owner.toggles, []byte{1, 0, 1, 0}) {
		t.Fatalf("zero-envelope transitions=%v", owner.toggles)
	}
}

func TestSessionSecureDecryptFailureClosesBoundOwner(t *testing.T) {
	encrypted, decryptor := secureEnvelopeFixture(t)
	for i := range encrypted[4:] {
		encrypted[4+i] ^= 0xff
		break
	}
	owner := &bodyProgressOwnerSpy{}
	session := newSession(nil)
	if err := session.BindInSegmentTimeout(owner); err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: &chunkConn{Reader: bytes.NewReader(encrypted), chunks: []int{4, len(encrypted)}}, secure: decryptor}
	session.wire = wire
	if _, err := wire.readWithHeaderObserverAndProgress(nil, session.bodyProgressCallbacks()); err == nil {
		t.Fatal("tampered secure envelope unexpectedly succeeded")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !owner.closed {
		t.Fatal("decrypt failure cleanup did not close bound owner")
	}
}

func TestSessionRejectsInSegmentBindingAfterReaderStarts(t *testing.T) {
	owner := &bodyProgressOwnerSpy{}
	session := newSession(nil)
	session.readLoopStarted = true
	if err := session.BindInSegmentTimeout(owner); err == nil {
		t.Fatal("binding after reader start unexpectedly succeeded")
	}
}
