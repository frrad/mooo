# In-segment read timeout contract

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

`toggleInSegmentTimeout:` has Objective-C type `v20@0:8B16` and implementation
`0x101773bdc`. It first reads the owner's configured in-segment timeout. When
that value is not strictly positive, it does not enqueue either branch. For a
positive value it queues one block on the main queue. The block tests the
captured enable byte exactly against `1`.

The enable branch rereads the timeout from the captured owner at block execution
and schedules `fireInSegmentTimeout` using the owner target and a nil object.
The disable branch does not reread the timeout; it cancels the exact owner,
`fireInSegmentTimeout`, nil-object tuple. The source forwards the execution-time
delay after the positive admission check, so a later zero or negative value is
represented as a scheduled operation in this contract. The block loads the owner
through a weak capture; owner lifetime and main-queue races remain gaps.

`fireInSegmentTimeout` has type `v16@0:8`, implementation `0x101773e10`, and
disconnects its owner. The callback's downstream error/fanout behavior is
covered by the separate socket-disconnect contract.

This is a delayed in-segment watchdog, separate from the socket read timeout. The
LocoAgent `readHeader` path requests socket data with timeout `-1.0`, length 4
when V2SL crypto is present or 22 otherwise, and tag 0. A nonzero header
length selects a body read using that length, socket timeout `-1.0`, and tag 1;
the body-read path enables the in-segment watchdog after scheduling that read.
Its partial-read
callback at `0x101774a3c` leaves tag-zero reads alone; for a nonzero tag it
queues `toggleInSegmentTimeout:NO` followed by `toggleInSegmentTimeout:YES`,
resetting the watchdog for continued body progress. Its complete-read callback
at `0x101774b3c` routes tag zero to header handling; for a nonzero tag it first
queues the disable operation, invokes `didReadBody:`, and then starts the next
`readHeader` operation. Body handling decrypts through the V2SL object when
present and supplies the resulting data to the packet producer; without crypto
it supplies the original data. The exact decrypted length interpretation,
packet-producer return values, and socket partial-read retry/error policy remain
outside this bounded contract. The separate TrailerAgent-family callbacks
around `0x1015f9654`/`0x1015f9688` are deliberately excluded.

## Static provenance

- `toggleInSegmentTimeout:` metadata and IMP: `0x101773bdc`; selector pointer
  `fireInSegmentTimeout` is used by the queued block.
- `fireInSegmentTimeout` metadata and IMP: `0x101773e10`; its reviewed body calls
  `disconnect` on the owner.
- The queue block is `0x101775088`; its enable/disable branch and exact selector
  tuple are visible there.
- LocoAgent class metadata maps the in-segment toggle and fire methods to the
  LocoAgent method table; the same table maps callbacks at `0x101774a3c` and
  `0x101774b3c` to LocoAgent. The separate TrailerAgent-family methods around
  `0x1015f9654`/`0x1015f9688` were excluded after class verification.

The fixtures are strict characterizations of the reviewed admission, reread,
queue, cancellation, terminal-disconnect, and LocoAgent read-routing behavior.
They do not claim a runtime timer implementation, decrypted wire-field
interpretation, or socket error policy.
