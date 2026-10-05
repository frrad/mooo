# V2 secure-layer AES-GCM contract

Status: reviewed clean-room partial specification, 2026-10-05.

This contract records a version-scoped, implementation-neutral observation of
the V2 secure-layer crypto primitive. The evidence is static binary analysis of
an authorized macOS client, transferred through sanitized experiment
`SL-BIN-V2SL-GCM-2026-10-05`. No account data, live key, packet capture, or
proprietary source is part of this repository.

## Primitive

For the selected V2 secure-layer implementation, encryption uses AES-128-GCM:

| Input | Contract |
| --- | --- |
| key | 16 bytes on the reviewed V2 path |
| nonce | 12 fresh bytes on the reviewed V2 path |
| associated data | absent (`nil`) |
| authentication tag | exactly 16 bytes |
| result | ciphertext and tag, with the tag supplied separately to the caller |

The outer V2 operation places the fresh nonce before the ciphertext and tag:

```text
nonce[12] || ciphertext[len(plaintext)] || tag[16]
```

The nonce is generated for each operation. The tag is collected from the GCM
operation after ciphertext generation; it is not a second encryption or a
checksum. The outer framing layer may add its own length prefix. That framing,
key provisioning, handshake acceptance, and transport response handling are
separate contracts and are intentionally not inferred here.

Decryption authenticates before exposing plaintext. A wrong key, nonce,
ciphertext, tag, or associated-data value fails authentication and returns no
plaintext; cleanup still runs before the primitive returns. Inputs with
unsupported key, nonce, or tag sizes are invalid.

The failure boundary is asymmetric. If the primitive cannot create its cipher
context, it returns no ciphertext. The observed outer V2 assembly has already
created its nonce and initialized a 16-byte tag destination at that point, so
it still concatenates the nonce, the absent ciphertext, and the unchanged tag
destination. This produces a nonce-plus-tag-sized framed value, not
authenticated application data, and a caller must reject it before interpreting
plaintext. On the reviewed path, the low-level operation statuses after context
creation are not individually branched on before tag collection; this is an
observed source behavior, not a recommendation to accept partially initialized
results. Decryption returns no plaintext after context or authentication
failure, including after its cleanup path.

This is a primitive contract, not a claim that all surrounding client paths
have been traced. In particular, the public evidence does not establish the
server's key-derivation or handshake policy, retry behavior, or every outer
failure-path allocation detail.

## Synthetic conformance

`internal/protocol/loco/v2sl_gcm_contract_test.go` uses only invented values
and the Go standard library. It checks a pinned `nonce || ciphertext || tag`
vector, successful round-trip, and rejection after changing the key, nonce,
ciphertext, tag, or associated data. A separate state fixture records the
observed outer fall-through when primitive context creation returns no
ciphertext. The strict key/nonce checks in the Go helper are synthetic
implementation guards; the source evidence establishes the widths supplied by
the reviewed path, not a complete invalid-size branch table. The vectors are
independent characterizations of the published byte layout, not live-client
captures.

## Confidence and scope

The algorithm, parameter widths, tag placement, and authentication behavior
have high confidence for the inspected V2 implementation. Outer packet
framing and handshake behavior remain separate, version-scoped work items.
