package loco

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

// This vector is the reviewed V2 AES-128-GCM primitive vector. SecureV3 adds
// its explicit four-byte little-endian envelope length before the reviewed
// nonce||ciphertext||tag bytes.
func TestSecureV3MatchesReviewedV2GCMVector(t *testing.T) {
	key := []byte("0123456789abcdef")
	nonce := []byte("123456789012")
	plaintext := []byte("synthetic V2SL payload")
	primitive, err := hex.DecodeString("313233343536373839303132b5282c81830bf3c252b1cd4e5c6b380505b3bfb5f4ff56868156ea70c1f6a167a79a30489082")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x32, 0x00, 0x00, 0x00}, primitive...)

	secure, err := NewSecureV3WithKey(key, bytes.NewReader(nonce), 1024)
	if err != nil {
		t.Fatal(err)
	}
	got, err := secure.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("SecureV3 envelope=%x want=%x", got, want)
	}
	plain, err := secure.Decrypt(got)
	if err != nil || !bytes.Equal(plain, plaintext) {
		t.Fatalf("SecureV3 decrypt=%q err=%v", plain, err)
	}
}

func TestSecureV3RejectsReviewedV2GCMAuthenticationMutations(t *testing.T) {
	key := []byte("0123456789abcdef")
	nonce := []byte("123456789012")
	plaintext := []byte("synthetic V2SL payload")
	secure, err := NewSecureV3WithKey(key, bytes.NewReader(nonce), 1024)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := secure.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func([]byte)
		key    []byte
	}{
		{name: "nonce", mutate: func(v []byte) { v[4] ^= 1 }, key: key},
		{name: "ciphertext", mutate: func(v []byte) { v[4+V3NonceSize] ^= 1 }, key: key},
		{name: "tag", mutate: func(v []byte) { v[len(v)-1] ^= 1 }, key: key},
		{name: "key", mutate: func([]byte) {}, key: []byte("fedcba9876543210")},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			candidate := append([]byte(nil), envelope...)
			tc.mutate(candidate)
			other, err := NewSecureV3WithKey(tc.key, bytes.NewReader(bytes.Repeat([]byte{0x44}, V3NonceSize)), 1024)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := other.Decrypt(candidate)
			if !errors.Is(err, ErrInvalidSecureEnvelope) || plain != nil {
				t.Fatalf("mutation plain=%q err=%v", plain, err)
			}
		})
	}
}

// The 49/50 limits exercise the implementation's configured envelope bound;
// they are not a claim about an independently traced official size limit.
func TestSecureV3EnforcesConfiguredEnvelopeBounds(t *testing.T) {
	key := []byte("0123456789abcdef")
	nonce := []byte("123456789012")
	plaintext := []byte("synthetic V2SL payload")
	tooSmall, err := NewSecureV3WithKey(key, bytes.NewReader(nonce), 49)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tooSmall.Encrypt(plaintext); !errors.Is(err, ErrCiphertextTooLarge) {
		t.Fatalf("small encryption limit error=%v", err)
	}
	valid, err := NewSecureV3WithKey(key, bytes.NewReader(nonce), 50)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := valid.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	tooSmallDecrypt, err := NewSecureV3WithKey(key, bytes.NewReader(nonce), 49)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tooSmallDecrypt.Decrypt(envelope); !errors.Is(err, ErrCiphertextTooLarge) {
		t.Fatalf("small decryption limit error=%v", err)
	}
}
