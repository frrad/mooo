# Raw receive parser contract

Status: reviewed static source contract; synthetic tests only. Provenance is
KakaoTalk 26.8.0 arm64. This document describes the observed
`supplyRawData:` parser boundary and does not activate runtime receipt or
transport behavior.

The recovered `supplyRawData:` implementation has return type `Q24@0:8@16`.
It appends the supplied data to its buffer. A buffer shorter than 22 bytes
returns zero without constructing a packet header, while retaining the
buffered-byte count for the next call. When no current header is present, it
initializes one from the buffered data and stores it. It invokes the optional
header producer immediately after header initialization, before checking
whether the body is complete. It then reads the header body length as the
source's unsigned 32-bit field and waits until the buffer contains
`bodyLength + 22` bytes; when incomplete, it returns the remaining byte count
and preserves the buffered length and current-header state. A complete frame clears the current header before packet-data initialization.

For a complete frame it clears the current header, initializes packet data,
removes the consumed bytes, and conditionally invokes delegate methods when the
delegate responds. The header producer runs in the new-header branch before
the body-completeness check; the packet producer runs after packet-data
initialization and consumption. Packet-initializer nil is represented
separately from the consumed-byte and delegate dispatch effects because the
raw call sequence has no observed guard that suppresses those later optional
delegate checks. Buffered trailing bytes begin a fresh header parse. The
synthetic multi-frame case supplies distinct per-frame header and packet-init
results, rather than reusing one global frame description. The
`frame_input_exhausted` result is a synthetic fixture-domain guard, not an
observed official stop or zero-return behavior.

Header initializer nil and malformed-header behavior require separate runtime
or lower-level source proof and are not generalized here; the bounded fixture
records only the observed nil-message boundary. Body lengths outside the
source's unsigned 32-bit domain are rejected by the typed fixture decoder.

Private provenance (not part of the repository) is the external parity receipt
`read-state-supply-data/report.txt` and its raw/decompiler extraction for IMP
`0x1013edf84`. Selector mappings resolve `buffer`, `length`,
`currentPacketHeader`, `initWithData:`, `bodyLength`, `initWithPacketData:`,
`replaceBytesInRange:withBytes:length:`, and the two producer selectors.
