# LocoAgent secure framing boundary

This contract covers the reviewed LocoAgent socket read boundary and the
LocoPacketProducer handoff. It describes observed branch effects and leaves
wire field interpretation to a later trace.

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
least 22 bytes before continuing and invokes its header-production callback only
once its own accumulation checks pass. The meaning of the 4/22 lengths, the
32-bit value, and producer return counts remain untraced.

These effects are implementation-neutral. A replacement may use a pure planner
with injected crypto-presence, tag, prefix value, and accumulated byte count;
it must preserve the observed branch ordering while keeping socket I/O,
cryptography, packet decoding, and callback delivery outside the reducer.
