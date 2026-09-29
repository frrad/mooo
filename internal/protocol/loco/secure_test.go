package loco

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestSecureV3RoundTripAndEnvelope(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, V3KeySize)
	nonce := bytes.Repeat([]byte{0x22}, V3NonceSize)
	s, err := NewSecureV3WithKey(key, bytes.NewReader(nonce), 1024)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("synthetic LOCO packet")
	envelope, err := s.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(envelope[:4]); got != uint32(V3NonceSize+len(plaintext)+V3TagSize) {
		t.Fatalf("envelope length = %d", got)
	}
	if !bytes.Equal(envelope[4:16], nonce) {
		t.Fatal("nonce layout mismatch")
	}
	got, err := s.Decrypt(envelope)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip: %q, %v", got, err)
	}
}

func TestSecureV3RejectsMutationAndLengthMismatch(t *testing.T) {
	s, _ := NewSecureV3WithKey(bytes.Repeat([]byte{1}, V3KeySize), bytes.NewReader(bytes.Repeat([]byte{2}, V3NonceSize)), 1024)
	envelope, _ := s.Encrypt([]byte("invented"))
	mutated := append([]byte(nil), envelope...)
	mutated[len(mutated)-1] ^= 1
	if _, err := s.Decrypt(mutated); !errors.Is(err, ErrInvalidSecureEnvelope) {
		t.Fatalf("mutation accepted: %v", err)
	}
	if _, err := s.Decrypt(envelope[:len(envelope)-1]); !errors.Is(err, ErrInvalidSecureEnvelope) {
		t.Fatalf("truncation accepted: %v", err)
	}
}

func TestSecureV3HandshakeShape(t *testing.T) {
	s, err := NewSecureV3(bytes.NewReader(bytes.Repeat([]byte{0x5a}, 2048)), 1024)
	if err != nil {
		t.Fatal(err)
	}
	handshake, err := s.Handshake()
	if err != nil {
		t.Fatal(err)
	}
	if len(handshake) != V3HandshakeSize || binary.LittleEndian.Uint32(handshake[0:4]) != 256 || binary.LittleEndian.Uint32(handshake[4:8]) != 16 || binary.LittleEndian.Uint32(handshake[8:12]) != 3 {
		t.Fatalf("bad handshake shape: len=%d prefix=%x", len(handshake), handshake[:12])
	}
}
