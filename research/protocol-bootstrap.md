# Initial protocol bootstrap observations

Status: preliminary binary-analysis specification, 2026-09-20.

This note describes behavior observed by constructing synthetic values inside an
authorized, logged-out macOS 26.8.0 client. No account, network request, captured
traffic, private message, live credential, or server response was involved. Raw
binary-analysis material remains outside the repository under the process in
`CLEANROOM.md`.

## Packet envelope

The client serializes a fixed 22-byte packet header:

| Offset | Size | Meaning | Encoding |
| ---: | ---: | --- | --- |
| 0 | 4 | Request/packet identifier | Unsigned little-endian integer |
| 4 | 2 | Response status | Unsigned little-endian integer |
| 6 | 11 | Operation name | Byte string, zero-padded to 11 bytes |
| 17 | 1 | Body encoding/type | Unsigned byte |
| 18 | 4 | Body length | Unsigned little-endian integer |

Confidence is high: a synthetic header containing asymmetric test values was
serialized locally and parsed byte-for-byte. Parser behavior for overlong operation
names, invalid lengths, and partial reads is not yet specified.

## Bootstrap bodies

Locally constructed configuration and check-in operations used body type `0`. The
body was a BSON document, and the BSON document length matched the header body
length. Numeric BSON values were little-endian.

The configuration request contained:

- a numeric user identifier;
- mobile-country/network metadata;
- an operating-system identifier.

The check-in request additionally contained:

- network type;
- application version;
- country and language;
- a boolean indicating secondary-device use.

These are field-presence observations, not claims about required values or server
policy. Field optionality, validation rules, and response semantics remain
unverified.

## Incremental packet parsing

The receive path buffers arbitrary chunks and applies this loop:

1. wait until at least 22 bytes are available;
2. parse the fixed header;
3. wait until `22 + body length` bytes are available;
4. emit one complete packet;
5. remove the consumed bytes and repeat.

This establishes support for fragmented and coalesced transport reads. Rejection
rules for implausible body lengths and invalid body types remain to be mapped.

## Secure layer

A fresh local secure-layer object generated 16 bytes of symmetric key material and
a 268-byte handshake value. Static control flow confirms that the key is encrypted
with RSA-OAEP using an embedded LOCO public key. The handshake serializes three
little-endian 32-bit values—encrypted-key length, plaintext-key length, and secure
layer type/version—followed by the RSA ciphertext. For the analyzed client these
values are 256, 16, and 3.

Secure mode encrypts the complete 22-byte-header-plus-body packet using
AES-128-GCM. Each outgoing packet uses a fresh 12-byte IV and produces:

```text
encrypted-length:u32-le || IV:12 || ciphertext:N || authentication-tag:16
```

The encrypted length describes the IV, ciphertext, and tag envelope. On receive,
the client reads the four-byte length, accumulates the indicated envelope, splits
those three components, decrypts them, and passes the recovered plaintext into the
same incremental packet parser. Without this secure layer, the transport reads the
22-byte plaintext header directly.

No compression stage was found in the core packet path. That absence has medium
confidence because compression dependencies may still serve other application
features or a path not reached by the inspected agents.

## Working bootstrap sequence

Static control flow establishes three distinct agent roles:

1. a booking agent requests configuration;
2. a ticket agent checks in and receives carriage endpoints;
3. a carriage agent establishes the persistent secure session and carries
   application commands.

The client maintains separate agents and address pools for these roles. The booking
stage has a concurrent-attempt guard and retries up to three times using jittered
backoff capped at eight seconds. Ticket attempts advance through an address pool
with jittered backoff capped at 32 seconds. Cached-endpoint eligibility, expiry, and
matching-failure invalidation are specified in `session-login/PROTOCOL.md`; exact
retry formulas and the complete terminal-error taxonomy remain open.

Configuration input includes the user identifier, mobile-country/network metadata,
and operating-system identifier. Its response groups settings for cellular and
Wi-Fi networks, ticket and trailer services, chat-log behavior, and a revision.

Check-in input additionally carries network type, application version, country,
language, and secondary-device status. Its response supplies IPv4/IPv6 carriage
hosts, port, cache lifetime, and separate secure-service host/port candidates.

The final carriage-login command is `LOGINLIST`. Its 17-field BSON request schema,
current access-token placement, response properties, status predicates, endpoint
cache, and recovery gates are now specified in `session-login/PROTOCOL.md`.

Registration handoff analysis now establishes that transient QR identifiers and
device-authorization codes are cleared before the common LOCO-login coordinator.
The downstream path consumes at least numeric user identity, an access token, and
foreground/background state. The current builder passes that prepared access-token
string unchanged as `oauthToken` and leaves `sKey` unset; see
`session-login/PROTOCOL.md` and `device-registration/PROTOCOL.md`.

## Next verification work

- recover body-type meanings and malformed-packet rejection rules;
- determine secure-layer type negotiation and fallback behavior;
- specify configuration and check-in responses;
- confirm unset-object BSON encoding and remaining response wire-key mappings;
- complete exact reconnect delays, kickout reasons, and cursor gap semantics.
