# Push-receipt write and completion chain

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

`LocoAgent` implementation `sendPacket:tag:` at `0x101773670` uses the same lower write path for receipt tags and ordinary
request tags. It obtains packet data, encrypts it, obtains the socket and packet
header/ID, then calls `writeData:withTimeout:tag:` with timeout `-1.0`. The
raw ABI loads the packet ID into the write-call tag register immediately before
the call (`0x101773854` → `0x101773858` → `0x101773868`); the caller-supplied
`tag` argument is not forwarded. The `mov w3,w0` at `0x101773858` zero-extends
the packet ID, so the lower write receives the nonnegative zero-extended uint32 packet ID for
both receipt and ordinary sends. The signed negative tag is derived earlier by
the receipt helper but is ignored by this `sendPacket:tag:` implementation.
It then calls `toggleOutSegmentTimeout:YES`. There is no sign test or
negative-tag branch in this method.

The observed `writeBinaryData` implementation synchronizes access to its
pending-request bookkeeping, computes the pending count, performs a branch for
pending state `2`, records the tag and associated values, and updates the
bookkeeping count. The available static body does not expose enough selector
identity to publish a storage schema or claim which completion consumer owns a
particular negative tag. Those are explicit gaps.

The socket connect/disconnect source shows a separate disconnect fanout path:
it schedules a callback block, transitions socket state, and has a state-2
branch that resets an associated object before continuing. The receipt-specific
write path does not expose a completion callback, acknowledgement parser, retry,
or persistent state update. No such behavior is inferred from the timeout toggle
or pending-map operations.

## Synthetic contract

The fixture compares receipt and ordinary calls with different caller tag
inputs but the same positive packet-ID-derived write tag. It also records the owner-status
suppression before packet access and the explicit `-1.0` write timeout / outbound
timeout toggle.
It does not invent a receipt acknowledgement or correlation result.

## Provenance

- `sendPacket:tag:` IMP `0x101773670`; packet data/encryption/socket/header/ID
  calls `0x1017737fc`, `0x101773814`, `0x101773830`, `0x101773844`,
  `0x101773854`; write call `0x101773868`; outbound timeout toggle
  `0x101773884`.
- `writeData:withTimeout:tag:` callsites include `sendPacket:tag:` at
  `0x101773868`, `writeBinaryData` at `0x1015f9088`/`0x1015f90c4`, and socket
  connect handling at `0x101774678`.
- `writeBinaryData` IMP `0x1015f8fb0`; socket connect/disconnect chain begins at
  `0x101774348`.
- private Ghidra reports: `~/Library/Application Support/mooo-lab/ghidra/parity/receipt-request-model/`, `socket-write/`, `write-binary-chain/`, and `socket-write-block/`.

The separate Swift-facing wrapper at `0x100d48dfc` delegates through `0x100d49560`; its ownership and dispatch relationship to the LocoAgent implementation remain a separate trace gap.

## Socket callback boundary

The LocoAgent delegate implementations expose the callback boundary without a
receipt-specific acknowledgement contract. `socket:didWriteDataWithTag:` at
`0x101774cb8` forwards the tag to `didWrite:` after a state/timeout update.
`socket:didReadData:withTag:` at `0x101774b3c` branches on zero versus nonzero
tag, performs the corresponding state update, and invokes `readHeader` on the
nonzero path. A separate implementation at `0x1015f99e8` synchronizes a
pending structure and branches on callback state; its exact class ownership is
not published here. The Swift wrapper `0x100d48dfc` delegates into
`0x100d49560`, whose `NWConnection.SendCompletion` handling is a distinct
implementation boundary.

The sign helper at `0x101773664` returns `((uint64 tag >> 63) ^ 1)`; no direct
branch reference to it was recovered in the inspected disassembly. The receipt
block's negative-tag derivation is therefore kept separate from the lower
LocoAgent write tag, and callback correlation remains unclaimed.
