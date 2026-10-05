# Raw receive parser contract

Status: reviewed static source contract; synthetic tests only. Provenance is
KakaoTalk 26.8.0 arm64. This document describes the observed
`supplyRawData:` parser boundary and does not activate runtime receipt or
transport behavior.

The recovered `supplyRawData:` implementation has return type `Q24@0:8@16`.
It appends the supplied data to its buffer. A buffer shorter than 22 bytes
returns zero without constructing a packet header. When no current header is
present, it initializes one from the buffered data and stores it. It invokes the optional header producer immediately after header initialization,
before checking whether the body is complete. It then reads the header body
length and waits until the buffer contains `bodyLength + 22` bytes;
when incomplete, it returns the remaining byte count.

For a complete frame it initializes packet data, removes the consumed bytes,
and conditionally invokes delegate methods when the delegate responds:
`locoPacketProducerProducePacketHeader:` first, then
`locoPacketProducerProducePacket:`. The current header is cleared as part of
consumption, and buffered trailing bytes begin a fresh header parse. The
parser loops while buffered bytes remain. A zero body length therefore uses a
22-byte complete-frame threshold.

The synthetic contract keeps these source inputs distinct: initial buffered
length, appended length, current-header presence, header initializer result,
body length, packet initializer result, and delegate method availability.
Packet initializer nil is represented separately from the consumed-byte and
delegate dispatch effects because the raw call sequence has no observed guard
that suppresses those later optional delegate checks. The header callback is
therefore observable even when the body remains incomplete. Header initializer nil
and malformed header behavior require separate runtime or lower-level source
proof and are not generalized here; the bounded fixture records only the
observed nil-message boundary.

Private provenance (not part of the repository) is the external parity receipt
`read-state-supply-data/report.txt` and its raw/decompiler extraction for IMP
`0x1013edf84`. Selector mappings resolve `buffer`, `length`,
`currentPacketHeader`, `initWithData:`, `bodyLength`, `initWithPacketData:`,
`replaceBytesInRange:withBytes:length:`, and the two producer selectors.
