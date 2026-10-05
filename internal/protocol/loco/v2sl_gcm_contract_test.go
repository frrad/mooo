package loco

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"testing"
)

const (
	v2SLKeySize   = 16
	v2SLNonceSize = 12
	v2SLTagSize   = 16
)

var errV2SLInvalidParameters = errors.New("v2sl: invalid AES-GCM parameters")

// v2SLSeal models only the reviewed primitive. Outer length prefixes and key
// provisioning are deliberately outside this synthetic contract.
func v2SLSeal(key, nonce, plaintext, aad []byte) ([]byte, error) {
	if len(key) != v2SLKeySize || len(nonce) != v2SLNonceSize {
		return nil, errV2SLInvalidParameters
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errV2SLInvalidParameters
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || gcm.Overhead() != v2SLTagSize {
		return nil, errV2SLInvalidParameters
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	return append(out, sealed...), nil
}

func v2SLOpen(key, envelope, aad []byte) ([]byte, error) {
	if len(key) != v2SLKeySize || len(envelope) < v2SLNonceSize+v2SLTagSize {
		return nil, errV2SLInvalidParameters
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errV2SLInvalidParameters
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || gcm.Overhead() != v2SLTagSize {
		return nil, errV2SLInvalidParameters
	}
	plaintext, err := gcm.Open(nil, envelope[:v2SLNonceSize], envelope[v2SLNonceSize:], aad)
	if err != nil {
		return nil, errV2SLInvalidParameters
	}
	return plaintext, nil
}

func TestV2SLGCMContractPinnedVector(t *testing.T) {
	key := []byte("0123456789abcdef")
	nonce := []byte("123456789012")
	plaintext := []byte("synthetic V2SL payload")
	want, err := hex.DecodeString("313233343536373839303132b5282c81830bf3c252b1cd4e5c6b380505b3bfb5f4ff56868156ea70c1f6a167a79a30489082")
	if err != nil {
		t.Fatal(err)
	}
	got, err := v2SLSeal(key, nonce, plaintext, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("V2 envelope = %x, want %x", got, want)
	}
	plain, err := v2SLOpen(key, got, nil)
	if err != nil || !bytes.Equal(plain, plaintext) {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
}

func TestV2SLGCMContractRejectsAuthenticationMutations(t *testing.T) {
	key := []byte("0123456789abcdef")
	nonce := []byte("123456789012")
	envelope, err := v2SLSeal(key, nonce, []byte("synthetic V2SL payload"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func([]byte)
		key    []byte
		aad    []byte
	}{
		{"nonce", func(b []byte) { b[0] ^= 1 }, key, nil},
		{"ciphertext", func(b []byte) { b[v2SLNonceSize] ^= 1 }, key, nil},
		{"tag", func(b []byte) { b[len(b)-1] ^= 1 }, key, nil},
		{"key", func([]byte) {}, []byte("fedcba9876543210"), nil},
		{"associated data", func([]byte) {}, key, []byte("unexpected AAD")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := append([]byte(nil), envelope...)
			tc.mutate(candidate)
			if plain, err := v2SLOpen(tc.key, candidate, tc.aad); err == nil || plain != nil {
				t.Fatalf("mutation accepted: plaintext=%q err=%v", plain, err)
			}
		})
	}
}

func TestV2SLGCMContractRejectsInvalidSizes(t *testing.T) {
	validKey := []byte("0123456789abcdef")
	validNonce := []byte("123456789012")
	for _, tc := range []struct {
		name  string
		key   []byte
		nonce []byte
	}{
		{"short key", validKey[:15], validNonce},
		{"long key", append(append([]byte(nil), validKey...), 0), validNonce},
		{"short nonce", validKey, validNonce[:11]},
		{"long nonce", validKey, append(append([]byte(nil), validNonce...), 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v2SLSeal(tc.key, tc.nonce, nil, nil); !errors.Is(err, errV2SLInvalidParameters) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := v2SLOpen(validKey, validNonce, nil); !errors.Is(err, errV2SLInvalidParameters) {
		t.Fatalf("short envelope error = %v", err)
	}
}
