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
	return s.encryptWithNonceUsing(nonce, plaintext, func(_ []byte, plaintext []byte) []byte {
		// The lifecycle fixture does not implement GCM; it models only the state
		// transition that precedes the already-published primitive contract.
		return append([]byte(nil), plaintext...)
	})
}

func (s *v2SLKeyIVState) encryptWithNonceUsing(nonce, plaintext []byte, primitive func([]byte, []byte) []byte) []byte {
	s.currentIV = append(s.currentIV[:0], nonce...)
	return primitive(append([]byte(nil), s.currentIV...), plaintext)
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

func TestV2SLCurrentIVIsPublishedBeforePrimitiveInvocation(t *testing.T) {
	key := bytes.Repeat([]byte{0x61}, 16)
	firstIV := bytes.Repeat([]byte{0x62}, 12)
	secondIV := bytes.Repeat([]byte{0x63}, 12)
	s := newV2SLKeyIVState(key)
	var seen []struct {
		iv       []byte
		material []byte
	}
	primitive := func(iv, plaintext []byte) []byte {
		seen = append(seen, struct {
			iv       []byte
			material []byte
		}{
			iv:       append([]byte(nil), iv...),
			material: append([]byte(nil), s.keyAndIVMaterial()...),
		})
		return append([]byte(nil), plaintext...)
	}

	s.encryptWithNonceUsing(firstIV, []byte("first"), primitive)
	s.encryptWithNonceUsing(secondIV, []byte("second"), primitive)
	if len(seen) != 2 {
		t.Fatalf("primitive calls = %d, want 2", len(seen))
	}
	wantFirst := append(append([]byte(nil), key...), firstIV...)
	wantSecond := append(append([]byte(nil), key...), secondIV...)
	if !bytes.Equal(seen[0].iv, firstIV) || !bytes.Equal(seen[0].material, wantFirst) {
		t.Fatalf("first primitive observation = iv %x material %x", seen[0].iv, seen[0].material)
	}
	if !bytes.Equal(seen[1].iv, secondIV) || !bytes.Equal(seen[1].material, wantSecond) {
		t.Fatalf("second primitive observation = iv %x material %x", seen[1].iv, seen[1].material)
	}
}

func TestV2SLHandshakeFramingSeparatesRSAResultFromKeyState(t *testing.T) {
	key := bytes.Repeat([]byte{0x44}, 16)
	s := newV2SLKeyIVState(key)
	rsaResult := make([]byte, 256)
	for i := range rsaResult {
		rsaResult[i] = byte(i)
	}
	frame := frameV2SLHandshake(rsaResult, s.keyAndIVMaterial(), 3)
	if len(frame) != 268 || binary.LittleEndian.Uint32(frame[:4]) != 256 || binary.LittleEndian.Uint32(frame[4:8]) != 16 || binary.LittleEndian.Uint32(frame[8:12]) != 3 {
		t.Fatalf("handshake frame = len %d prefix %x", len(frame), frame[:12])
	}
	want := append([]byte{0x00, 0x01, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00}, rsaResult...)
	if !bytes.Equal(frame, want) {
		t.Fatalf("handshake bytes differ: got prefix/body %x/%x", frame[:12], frame[12:20])
	}

	// A nil RSA result is represented as an empty result by this framing model;
	// the fixture deliberately does not infer whether a caller should retry or
	// fail because that consumer contract is not traced here.
	failed := frameV2SLHandshake(nil, s.keyAndIVMaterial(), 3)
	if len(failed) != 12 || binary.LittleEndian.Uint32(failed[:4]) != 0 || binary.LittleEndian.Uint32(failed[4:8]) != 16 || binary.LittleEndian.Uint32(failed[8:12]) != 3 {
		t.Fatalf("nil-result frame = len %d prefix %x", len(failed), failed)
	}
}
