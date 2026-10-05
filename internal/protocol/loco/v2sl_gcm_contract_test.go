package loco

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"reflect"
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

// observedV2SLOuterAssembly models the source-driven state boundary around
// the primitive. The outer operation owns the nonce and tag destination and
// does not discard them merely because the primitive produced no ciphertext.
func observedV2SLOuterAssembly(nonce, initialTag []byte, primitive func([]byte) []byte) []byte {
	tag := append([]byte(nil), initialTag...)
	ciphertext := primitive(tag)
	out := make([]byte, 0, len(nonce)+len(ciphertext)+len(tag))
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return append(out, tag...)
}

type v2SLPrimitiveTrace struct {
	contextCreated bool
	statuses       []int
	steps          []string
	seenStatuses   []int
	effects        []string
	finalCode      int
	freed          bool
}

// observedV2SLPrimitiveState records the reviewed control-flow boundary. The
// primitive stops at context creation failure; once a context exists, later
// low-level statuses do not short-circuit the sequence. The two initialization
// calls and IV-length control are separate stages intentionally.
func observedV2SLPrimitiveState(trace *v2SLPrimitiveTrace, tag []byte) []byte {
	trace.effects = append(trace.effects, "context")
	trace.steps = append(trace.steps, "context")
	if !trace.contextCreated {
		return nil
	}
	for i, step := range []string{"init-cipher", "set-iv-length", "init-key-iv", "update", "final", "get-tag"} {
		trace.steps = append(trace.steps, step)
		trace.effects = append(trace.effects, step)
		if i < len(trace.statuses) {
			trace.seenStatuses = append(trace.seenStatuses, trace.statuses[i])
		}
	}
	copy(tag, bytes.Repeat([]byte{0x3c}, len(tag)))
	trace.effects = append(trace.effects, "free")
	trace.freed = true
	return []byte("ciphertext")
}

func observedV2SLDecryptState(trace *v2SLPrimitiveTrace, plaintext []byte) []byte {
	trace.effects = append(trace.effects, "context")
	if !trace.contextCreated {
		return nil
	}
	for i, step := range []string{"init-cipher", "set-iv-length", "init-key-iv", "decrypt-update", "set-tag", "decrypt-final"} {
		trace.steps = append(trace.steps, step)
		trace.effects = append(trace.effects, step)
		if i < len(trace.statuses) {
			trace.seenStatuses = append(trace.seenStatuses, trace.statuses[i])
		}
	}
	trace.effects = append(trace.effects, "free")
	trace.freed = true
	trace.effects = append(trace.effects, "predicate")
	if trace.finalCode < 1 {
		return nil
	}
	trace.effects = append(trace.effects, "publish")
	return append([]byte(nil), plaintext...)
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

func TestV2SLOuterAssemblyRetainsTagWhenPrimitiveHasNoCiphertext(t *testing.T) {
	nonce := []byte("123456789012")
	initialTag := bytes.Repeat([]byte{0xa5}, v2SLTagSize)
	out := observedV2SLOuterAssembly(nonce, initialTag, func(tag []byte) []byte {
		// Synthetic context-creation failure: no ciphertext and no tag write.
		return nil
	})
	if len(out) != v2SLNonceSize+v2SLTagSize {
		t.Fatalf("failure envelope length = %d, want %d", len(out), v2SLNonceSize+v2SLTagSize)
	}
	if !bytes.Equal(out[:v2SLNonceSize], nonce) || !bytes.Equal(out[v2SLNonceSize:], initialTag) {
		t.Fatalf("failure envelope = %x", out)
	}

	writtenTag := bytes.Repeat([]byte{0x3c}, v2SLTagSize)
	out = observedV2SLOuterAssembly(nonce, initialTag, func(tag []byte) []byte {
		copy(tag, writtenTag)
		return []byte("ciphertext")
	})
	want := append(append(append([]byte(nil), nonce...), []byte("ciphertext")...), writtenTag...)
	if !bytes.Equal(out, want) {
		t.Fatalf("successful envelope state = %x", out)
	}
}

func TestV2SLPrimitiveFailureStateAndIgnoredStatuses(t *testing.T) {
	tag := bytes.Repeat([]byte{0xa5}, v2SLTagSize)
	failed := &v2SLPrimitiveTrace{contextCreated: false}
	if got := observedV2SLPrimitiveState(failed, tag); got != nil || !bytes.Equal(tag, bytes.Repeat([]byte{0xa5}, v2SLTagSize)) || !reflect.DeepEqual(failed.steps, []string{"context"}) {
		t.Fatalf("context failure state: output=%x tag=%x steps=%v", got, tag, failed.steps)
	}
	active := &v2SLPrimitiveTrace{contextCreated: true, statuses: []int{-1, 0, -1, 0, -1, 0}}
	if got := observedV2SLPrimitiveState(active, tag); !bytes.Equal(got, []byte("ciphertext")) || !reflect.DeepEqual(active.steps, []string{"context", "init-cipher", "set-iv-length", "init-key-iv", "update", "final", "get-tag"}) || !reflect.DeepEqual(active.seenStatuses, active.statuses) || !active.freed {
		t.Fatalf("post-context state: output=%x steps=%v", got, active.steps)
	}
	if !reflect.DeepEqual(active.effects, []string{"context", "init-cipher", "set-iv-length", "init-key-iv", "update", "final", "get-tag", "free"}) {
		t.Fatalf("encryption effects = %v", active.effects)
	}
	noContext := &v2SLPrimitiveTrace{contextCreated: false, finalCode: 1}
	if got := observedV2SLDecryptState(noContext, []byte("plaintext")); got != nil || noContext.freed {
		t.Fatalf("context failure decrypt state: output=%q freed=%v", got, noContext.freed)
	}
	for _, code := range []int{-1, 0, 1, 2} {
		trace := &v2SLPrimitiveTrace{contextCreated: true, finalCode: code, statuses: []int{0, -1, 0, -1, 0, -1}}
		got := observedV2SLDecryptState(trace, []byte("plaintext"))
		if (code < 1) != (got == nil) || !trace.freed {
			t.Fatalf("final code %d: output=%q freed=%v", code, got, trace.freed)
		}
		wantEffects := []string{"context", "init-cipher", "set-iv-length", "init-key-iv", "decrypt-update", "set-tag", "decrypt-final", "free", "predicate"}
		if code >= 1 {
			wantEffects = append(wantEffects, "publish")
		}
		if !reflect.DeepEqual(trace.effects, wantEffects) {
			t.Fatalf("final code %d effects = %v", code, trace.effects)
		}
		if !reflect.DeepEqual(trace.seenStatuses, trace.statuses) {
			t.Fatalf("final code %d statuses = %v", code, trace.seenStatuses)
		}
	}
}
