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
| key | exactly 16 bytes |
| nonce | exactly 12 fresh bytes per encryption |
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
created its nonce and tag destination at that point, so it can still assemble
the nonce plus the unchanged tag destination around the absent ciphertext.
This is a framed failure result, not authenticated application data, and a
caller must reject it before interpreting plaintext. After a context exists,
the reviewed encryption path does not branch on every low-level operation
status before collecting the tag; implementations should therefore preserve
the documented authenticated-output checks rather than silently accepting a
partially initialized result. Decryption returns no plaintext on context or
authentication failure.

This is a primitive contract, not a claim that all surrounding client paths
have been traced. In particular, the public evidence does not establish the
server's key-derivation or handshake policy, retry behavior, or every outer
failure-path allocation detail.

## Synthetic conformance

`internal/protocol/loco/v2sl_gcm_contract_test.go` uses only invented values
and the Go standard library. It checks a pinned `nonce || ciphertext || tag`
vector, successful round-trip, and rejection after changing the key, nonce,
ciphertext, tag, or associated data. It also checks the parameter-size guards.
The vector is an independent characterization of the published byte layout;
it is not a live-client capture.

## Confidence and scope

The algorithm, parameter widths, tag placement, and authentication behavior
have high confidence for the inspected V2 implementation. Outer packet
framing and handshake behavior remain separate, version-scoped work items.
