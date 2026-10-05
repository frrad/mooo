package loco

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type v2SLKeyIVState struct {
	key       []byte
	currentIV []byte
}

func newV2SLKeyIVState(key []byte) *v2SLKeyIVState {
	return &v2SLKeyIVState{key: append([]byte(nil), key...)}
}

func (s *v2SLKeyIVState) keyAndIVMaterial() []byte {
	out := append([]byte(nil), s.key...)
	return append(out, s.currentIV...)
}

func (s *v2SLKeyIVState) encryptWithNonce(nonce, plaintext []byte) []byte {
	s.currentIV = append(s.currentIV[:0], nonce...)
	// The lifecycle fixture does not implement GCM; it models only the state
	// transition that precedes the already-published primitive contract.
	return append([]byte(nil), plaintext...)
}

func frameV2SLHandshake(encryptedKey, keyMaterial []byte, layerType uint32) []byte {
	out := make([]byte, 12)
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(encryptedKey)))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(keyMaterial)))
	binary.LittleEndian.PutUint32(out[8:12], layerType)
	return append(out, encryptedKey...)
}

func TestV2SLKeyIVMaterialLifecycle(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	firstIV := bytes.Repeat([]byte{0x22}, 12)
	secondIV := bytes.Repeat([]byte{0x33}, 12)
	s := newV2SLKeyIVState(key)

	if got := s.keyAndIVMaterial(); !bytes.Equal(got, key) {
		t.Fatalf("pre-encrypt material = %x, want key-only %x", got, key)
	}
	s.encryptWithNonce(firstIV, []byte("synthetic packet"))
	want := append(append([]byte(nil), key...), firstIV...)
	if got := s.keyAndIVMaterial(); !bytes.Equal(got, want) {
		t.Fatalf("first post-encrypt material = %x, want %x", got, want)
	}
	s.encryptWithNonce(secondIV, []byte("second packet"))
	want = append(append([]byte(nil), key...), secondIV...)
	if got := s.keyAndIVMaterial(); !bytes.Equal(got, want) {
		t.Fatalf("second post-encrypt material = %x, want %x", got, want)
	}
}

func TestV2SLHandshakeFramingSeparatesRSAResultFromKeyState(t *testing.T) {
	key := bytes.Repeat([]byte{0x44}, 16)
	s := newV2SLKeyIVState(key)
	frame := frameV2SLHandshake(bytes.Repeat([]byte{0x55}, 256), s.keyAndIVMaterial(), 3)
	if len(frame) != 268 || binary.LittleEndian.Uint32(frame[:4]) != 256 || binary.LittleEndian.Uint32(frame[4:8]) != 16 || binary.LittleEndian.Uint32(frame[8:12]) != 3 {
		t.Fatalf("handshake frame = len %d prefix %x", len(frame), frame[:12])
	}

	// A nil RSA result is represented as an empty result by this framing model;
	// the fixture deliberately does not infer whether a caller should retry or
	// fail because that consumer contract is not traced here.
	failed := frameV2SLHandshake(nil, s.keyAndIVMaterial(), 3)
	if len(failed) != 12 || binary.LittleEndian.Uint32(failed[:4]) != 0 || binary.LittleEndian.Uint32(failed[4:8]) != 16 || binary.LittleEndian.Uint32(failed[8:12]) != 3 {
		t.Fatalf("nil-result frame = len %d prefix %x", len(failed), failed)
	}
}
