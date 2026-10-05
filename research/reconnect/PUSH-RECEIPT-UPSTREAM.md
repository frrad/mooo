# Push-receipt upstream eligibility

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

The only Objective-C call sites found for `sendCarriagePushReceipt:` are the
push notice handlers `handleHintPushNotice:packetHeader:` and
`handleBlockSyncPushNotice:packetHeader:`. Both retain the notice and packet
header, optionally notify a delegate when it responds to the corresponding
notice callback, construct the notice packet from the supplied header, and then
invoke `sendCarriagePushReceipt:` with that packet. The receipt call follows
packet construction and the delegate callback attempt in both handlers.

The hint handler invokes `locoManager:didReceiveHintPushNotice:` when its
captured delegate responds to that selector. The block-sync handler invokes
`locoManager:didReceiveBlockSyncPushNotice:` under the same responds-to-selector
guard. The receipt call itself is not nested under those delegate guards, so a
missing delegate callback does not suppress receipt submission in the reviewed
source. The reviewed handlers contain no explicit packet-header nil or packet
construction failure branch; lower initializer and send-path failures remain
separate gaps.

The receipt owner then applies its own execution-time status gate and packet
header/ID requirements; this document records only eligibility and ordering in
these two upstream consumers. It does not activate a runtime receipt default.

The receipt gate's status width is also observed: the LocoAgent `status` getter
has Objective-C type `c16@0:8` at IMP `0x10151c384` and returns a byte loaded
from receiver offset `+8`. The queued receipt block independently performs
`ldrb w8, [owner, #8]` at `0x101775204` and compares that byte with `3` at
`0x101775208`. The public contract therefore models an unsigned storage byte,
with no narrowing from a wider status enum.

## Synthetic contract

The fixture records the two discovered handlers, delegate-present and
-delegate-present paths, and the required packet-header/packet-construction
ordering. It keeps the delegate callback and receipt submission as separate
effects so a future implementation cannot accidentally suppress the receipt
when the optional delegate is absent.

## Provenance

- `handleHintPushNotice:packetHeader:` IMP `0x101515264`; receipt callsite
  `0x101515350`.
- `handleBlockSyncPushNotice:packetHeader:` IMP `0x101517988`; receipt callsite
  `0x101517a98`.
- selector inventory query: private Ghidra `MoooQuery objc-calls
  sendCarriagePushReceipt:`; exactly two callers.
- delegate selectors: `locoManager:didReceiveHintPushNotice:` at callsite
  `0x101515128`, and `locoManager:didReceiveBlockSyncPushNotice:` at
  `0x101517a50`.
- packet initializers: `initWithPacketHeader:` at `0x101515338`, and
  `initWithPacketHeader:revision:plusRevision:` at `0x101517a88`.
- LocoAgent `status` metadata/body: type `c16@0:8`, IMP `0x10151c384`,
  `return (char *)(self + 8)`; receipt gate raw load/compare:
  `0x101775204`/`0x101775208`.
- private receipts are retained outside the repository under
  `~/Library/Application Support/mooo-lab/ghidra/parity/carriage-push-upstream/`.
