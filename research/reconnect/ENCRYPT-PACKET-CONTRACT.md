# Base `encryptPacketData:` contract

Status: reviewed static source contract; synthetic tests only. This document
describes the base `LocoAgent` implementation and is separate from the selected
`LocoNWAgent` wrapper's optional-encryption path.

The Objective-C method metadata identifies `encryptPacketData:` as an object
method receiving one data object. The base `sendPacket:tag:` implementation
calls it after `packetData` and before socket write. The selected `LocoNWAgent`
wrapper also reaches this inherited method after its own packet-data/encryption
guards; its Optional.none path is a separate wrapper contract and can still
send when a connection is present. The owner reads `_v2slCrypto` at ivar offset
`0x18`.

When that field is absent, the method retains and returns its input object by
identity. A nil input therefore remains nil. No mutable output allocation or
crypto call occurs on that branch.

When the field is present, the method first allocates an `NSMutableData`, calls
the crypto object's `encrypt:` selector, reads the returned object's `length`,
stores that value through a 32-bit `w0` load/store, appends exactly four bytes
from the stored word, and then calls `appendData:` with the crypto result. The
ARM64 store makes the observed prefix little-endian on the reviewed platform;
the source also truncates lengths modulo 2^32. The synthetic vector includes a
length above 2^32 to keep this truncation input-derived.

The `encrypt:` receiver is the `LocoV2SLCrypto` object. Its reviewed IMP is
`0x1016857f8`; it creates a mutable result, generates a 12-byte IV and stores it
through `setAesIV:`, creates a 16-byte mutable tag buffer, reads the object's
AES key and IV, and calls `encryptAES128GCMWithKey:iv:aad:tag:` with AAD equal
to nil and the mutable tag buffer. It appends the generated IV first, then the
returned cipher result, then the tag buffer. The resulting helper output order
is IV, cipher result, tag. The algorithm implementation and key material remain
outside this public contract; the outer framing contract treats the returned
data object as an input-derived opaque result.

The crypto result's nil and nonnil-zero-length cases are distinct source inputs.
The reviewed Foundation probe on macOS 26.6.2 accepts `appendData:nil` and
leaves the four-byte zero prefix; that is recorded as a platform-scoped probe,
not an app-server or cross-platform guarantee. If the mutable allocation itself
is nil, the method still attempts both append messages to that nil receiver and
returns the nil container; the raw method has no allocation-result guard.

Private provenance (not part of the repository):

- `rc-q5-encrypt-method/decompile.txt` and `rc-q5-encrypt-method/report.txt`
  identify IMP `0x1017738ac`, the `_v2slCrypto` guard, `encrypt:`, `length`,
  `appendBytes:length:`, and `appendData:` calls.
- The raw ARM64 extraction in `credential-storage/raw/cs1/disassembly.txt`
  covers `0x1017738ac` through its return and shows the `str w0` prefix store;
  the corresponding `objc-stubs-disassembly.txt` only resolves selector stubs.
- `rc-q5-send-order-encrypt-callers.txt` records the recognized caller.
- `rc-q5-agent-status/locoagent-ivars.txt` records `_v2slCrypto` and its
  `LocoV2SLCrypto` type.
- `rc-q5-v2sl-encrypt/selector-query.txt` maps `encrypt:` to IMP `0x1016857f8`,
  and `rc-q5-v2sl-encrypt/raw-disasm.txt` records its fixed-size `NSData`
  constructors, IV setter, key/IV getters, nil AAD register, and AES-GCM
  selector call.
- `parent-foundation-append-nil/probe.m` is the platform-scoped Foundation
  append probe.

The synthetic fixture `rc-q5-encrypt-packet.json` checks input-derived identity,
nil/empty result distinction, little-endian framing, 32-bit truncation, and the
nil-container append attempts. It does not claim the crypto algorithm or key
schedule.
