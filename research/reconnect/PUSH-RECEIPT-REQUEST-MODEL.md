# Push-receipt request model

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

The HINT handler constructs a request with `initWithPacketHeader:`. That
initializer calls the base initializer, copies the supplied header's `method`
into the request method, and copies its `packetId` into the request packet ID.
The BLOCKSYNC handler constructs a request with
`initWithPacketHeader:revision:plusRevision:`. Its base initializer receives the
header; the initializer then stores the supplied revision and plus-revision
integers. The two handlers invoke their delegate callback attempt before these
request constructors and call `sendCarriagePushReceipt:` only after construction.

`sendPushReceipt:` enqueues work on the LocoAgent owner queue. When that block
runs, it reads the weak owner's status byte and proceeds only for value `3`.
It obtains the request packet header and packet ID, derives the signed tag by
negating the unsigned 32-bit packet ID, and calls `sendPacket:tag:`. The
reviewed `sendPacket:tag:` path then checks the packet method against its skip
set, obtains packet data, encrypts it, obtains the socket and header/ID, writes
with timeout and tag, and toggles the outbound-segment timeout. The socket
completion, write failure callback, and acknowledgement state consumer are not
traced in this bounded source chain.

No source evidence here establishes a retry, receipt acknowledgement, or
persistent state update. The only upstream stateful effects established are
request construction and the outbound write/timeout calls. Header absence,
constructor failure, and socket/write failure behavior remain explicit gaps
because the reviewed constructors and lower send path expose no complete error
contract.

## Provenance

- HINT request initializer: `initWithPacketHeader:` IMP `0x101355a60`;
  method/packet-ID field calls at `0x101355aac` and `0x101355ad4`.
- BLOCKSYNC request initializer: `initWithPacketHeader:revision:plusRevision:`
  IMP `0x1016bb15c`; revision stores at `0x1016bb1a4` and `0x1016bb1b0`.
- `sendPushReceipt:` IMP `0x1017734f4`; owner block `0x1017751d8`.
- `sendPacket:tag:` implementations: `0x100d48dfc` and `0x101773670`.
- packet data/encryption/socket/write/timeout callsites:
  `0x1017737fc`, `0x101773814`, `0x101773830`, `0x101773868`,
  `0x101773884`.
- private Ghidra report: `~/Library/Application Support/mooo-lab/ghidra/parity/receipt-request-model/`.
