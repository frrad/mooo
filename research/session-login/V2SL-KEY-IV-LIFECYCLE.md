# V2 secure-layer key and IV lifecycle

Status: reviewed clean-room partial specification, 2026-10-04.

This note records a version-scoped static observation from an authorized macOS
26.8.0 client. The sanitized provenance ID is
`SL-BIN-V2SL-KEY-IV-LIFECYCLE-2026-10-04`; the private transfer receipt is
`v2sl-key-iv-lifecycle-2026-10-04`. No account data, live key, packet capture,
or proprietary source is included here.

## State transitions

The selected V2 secure-layer object generates a 16-byte symmetric key during
initialization. Its current-IV property is initially absent. The handshake key
material accessor appends the key and then appends the current IV when one is
present. Consequently, the pre-encryption material is key-only (16 bytes).

Each encryption generates a fresh 12-byte IV and stores it as the current IV
before invoking the AES-128-GCM primitive. The synthetic contract injects a
primitive observer and checks the state visible at both invocations. After the first encryption, the
accessor returns `key || current-IV` (28 bytes). A later encryption replaces
the current IV; it does not append a history of nonces. The retained property
is object state and is distinct from the per-message freshness requirement.

The handshake framing records the encrypted-result length, key-material length,
and secure-layer type as little-endian 32-bit values before the encrypted
result. The observed successful values are `256`, `16`, and `3`, followed by
the opaque RSA result. The synthetic fixture compares the complete frame bytes
against that little-endian prefix plus a 256-byte distinct synthetic RSA body.
It also records the mechanical framing of
an empty RSA result, but does not classify that result as success, retry, or
fatal failure because the downstream consumer was not traced.

## Synthetic contract

`internal/protocol/loco/v2sl_key_iv_lifecycle_test.go` uses invented key,
nonce, plaintext, and RSA-result bytes. It proves the pre-encryption key-only
state, post-encryption key-plus-current-IV state, replacement on a second
encryption, successful handshake framing, and neutral empty-result framing.
The GCM byte recipe and authentication/failure contract are specified
separately in `V2SL-GCM.md`.

## Scope and gaps

This contract establishes state ordering and framing fields. It does not claim
that a handshake is sent before all later state reads, does not specify server
acceptance, and does not infer caller behavior from an empty RSA result.
