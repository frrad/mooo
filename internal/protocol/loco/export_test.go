package loco

import (
	"crypto/rand"
	"io"
)

// NewSecureV3WithKey builds a SecureV3 around a synthetic key for loco's own
// tests. Tests outside this package use testsupport/loco.NewKeyedSecureV3.
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
