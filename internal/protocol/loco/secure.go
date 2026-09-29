package loco

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // LOCO v3 uses the reviewed client's OAEP default.
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	V3KeySize            = 16
	V3NonceSize          = 12
	V3TagSize            = 16
	V3Type               = uint32(3)
	V3HandshakeSize      = 268
	DefaultMaxCiphertext = DefaultMaxBody + HeaderSize + V3NonceSize + V3TagSize
)

var (
	ErrInvalidKey            = errors.New("loco: invalid secure-layer key")
	ErrInvalidSecureEnvelope = errors.New("loco: invalid secure envelope")
	ErrCiphertextTooLarge    = errors.New("loco: ciphertext too large")
)

// Versioned 2048-bit PKCS#1 public key embedded by macOS 26.8.0. This is public
// protocol material and contains no account state.
const v3PublicKeyDERBase64 = "MIIBCAKCAQEAo7B26MRFhR8ZpnDCMarG20Lv0JcX0GBIpcxWkGzRqye53zf/1QF+fBOhQFtdHD5IeaakmdPGGKckcrC1DKXvHvbupwNp2UE/5mLY4rR5qfchQu5wzubCrRIEXVKyXEogSiiWjjfwumpJ7j7J8qx6ZRhBYPIvYsQ6QGfNjSpvE9m4KYqwAnY9I2ydGHnX/OW4+pEIgrIeFSR+DQokeRMI5RmDYUQC6foDBXxX6eF4scw5/mcojvxGGUXLyqEdH8wSPnULhh8NRH6+PBFfQRpC3JXdsh2kJ3SlvLHd9/pfEGKAEMdPNvMcQO/P4on9gbq6RKZVamwwEhBBS2Ajw/RjcQIBAw=="

type SecureV3 struct {
	key     [V3KeySize]byte
	random  io.Reader
	maxSize uint32
}

func NewSecureV3(random io.Reader, maxCiphertext uint32) (*SecureV3, error) {
	if random == nil {
		random = rand.Reader
	}
	if maxCiphertext == 0 {
		maxCiphertext = DefaultMaxCiphertext
	}
	s := &SecureV3{random: random, maxSize: maxCiphertext}
	if _, err := io.ReadFull(random, s.key[:]); err != nil {
		return nil, fmt.Errorf("generate secure-layer key: %w", err)
	}
	return s, nil
}

// NewSecureV3WithKey exists for synthetic fixtures and a receiver that already
// owns negotiated key material. It copies the key and never retains the input.
func NewSecureV3WithKey(key []byte, random io.Reader, maxCiphertext uint32) (*SecureV3, error) {
	if len(key) != V3KeySize {
		return nil, ErrInvalidKey
	}
	if random == nil {
		random = rand.Reader
	}
	if maxCiphertext == 0 {
		maxCiphertext = DefaultMaxCiphertext
	}
	s := &SecureV3{random: random, maxSize: maxCiphertext}
	copy(s.key[:], key)
	return s, nil
}

func (s *SecureV3) Handshake() ([]byte, error) {
	if s == nil {
		return nil, ErrInvalidKey
	}
	der, err := base64.StdEncoding.DecodeString(v3PublicKeyDERBase64)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	publicKey, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	encrypted, err := rsa.EncryptOAEP(sha1.New(), s.random, publicKey, s.key[:], nil)
	if err != nil {
		return nil, fmt.Errorf("wrap secure-layer key: %w", err)
	}
	out := make([]byte, 12+len(encrypted))
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(encrypted)))
	binary.LittleEndian.PutUint32(out[4:8], V3KeySize)
	binary.LittleEndian.PutUint32(out[8:12], V3Type)
	copy(out[12:], encrypted)
	return out, nil
}

func (s *SecureV3) Encrypt(plaintext []byte) ([]byte, error) {
	gcm, err := s.gcm()
	if err != nil {
		return nil, err
	}
	sealedLen := uint64(V3NonceSize) + uint64(len(plaintext)) + uint64(gcm.Overhead())
	if sealedLen > uint64(s.maxSize) {
		return nil, ErrCiphertextTooLarge
	}
	nonce := make([]byte, V3NonceSize)
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return nil, fmt.Errorf("generate secure-layer nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 4, 4+len(nonce)+len(sealed))
	binary.LittleEndian.PutUint32(out[:4], uint32(len(nonce)+len(sealed)))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

func (s *SecureV3) Decrypt(envelope []byte) ([]byte, error) {
	if len(envelope) < 4+V3NonceSize+V3TagSize {
		return nil, ErrInvalidSecureEnvelope
	}
	length := binary.LittleEndian.Uint32(envelope[:4])
	if length > s.maxSize {
		return nil, ErrCiphertextTooLarge
	}
	if uint64(length)+4 != uint64(len(envelope)) {
		return nil, ErrInvalidSecureEnvelope
	}
	gcm, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := envelope[4 : 4+V3NonceSize]
	ciphertext := envelope[4+V3NonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidSecureEnvelope
	}
	return plaintext, nil
}

func (s *SecureV3) gcm() (cipher.AEAD, error) {
	if s == nil {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, ErrInvalidKey
	}
	return cipher.NewGCM(block)
}

// KeyForTesting returns a copy for synthetic cross-instance tests.
func (s *SecureV3) KeyForTesting() []byte {
	if s == nil {
		return nil
	}
	out := make([]byte, V3KeySize)
	copy(out, s.key[:])
	return out
}
