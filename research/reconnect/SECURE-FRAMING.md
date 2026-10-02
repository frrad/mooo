# LocoAgent secure framing boundary

This contract covers the reviewed LocoAgent socket read boundary and the
LocoPacketProducer handoff. It describes observed branch effects and leaves
wire field interpretation to a later trace.

The reviewed agent initializer allocates a packet producer and calls its
`initWithDelegate:` with the agent as delegate. The producer initializer stores
that delegate, creates a mutable accumulation buffer, and starts with no current
header. This owner/delegate relationship is observed; the pure planner should
represent callback capability as an injected boundary.

The LocoAgent read callback receives a tag. Tag `0` dispatches header data to
`didReadHeader:`; nonzero tags dispatch to body handling. Its header reader
checks whether the V2SL crypto object is present, then asks the socket for a
header read with timeout `-1` and tag `0`: the requested length is `4` when
crypto is present and `22` otherwise.

With crypto present, `didReadHeader:` reads one unsigned 32-bit value from the
received data. Zero returns to header reading; nonzero enters body-length
handling. Without crypto, the received data is supplied directly to the packet
producer. Body handling decrypts accumulated data when crypto is present, then
supplies the resulting data to the packet producer. The producer requires at
least 22 accumulated bytes before continuing. When no current header exists, it
constructs one and conditionally invokes the header-production callback if the
delegate responds to that selector. It then computes the body requirement from
the parsed header and only invokes the complete-packet callback after the
accumulated data covers that requirement. Both callback deliveries are guarded
by the delegate's selector support. The producer reaches these callbacks
through its delegate; the constructor and delegate assignment are part of the
reviewed chain. Thus header production precedes
complete-packet production; timeout disarm is a separate downstream effect of
header production when the packet identity guard matches, and is not asserted
for every header or body.

The producer retains its accumulation buffer and loops while another complete
frame is available; split bodies remain buffered for a later supply. The packet
header decoder consumes the first 22 bytes as: unsigned 32-bit packet ID,
unsigned 16-bit status, an 11-byte UTF-8 method field, an unsigned 8-bit body
type, and an unsigned 32-bit body length. The producer compares accumulated
length with 22 plus that body length. Short-input exception behavior and any
method-string validation remain untraced.

These effects are implementation-neutral. A replacement may use a pure planner
with injected crypto-presence, tag, prefix value, and accumulated byte count;
it must preserve the observed branch ordering while keeping socket I/O,
cryptography, packet decoding, and callback delivery outside the reducer.
