# Carriage startup timeout provenance

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

The reviewed booking/configuration initializer supplies four timeout values in
seconds as floating point values: connect timeout `15`, receive-header timeout
`20`, in-segment timeout `10`, and out-segment timeout `10`. These are values at
one observed call site, not universal defaults. The initializer stores them in
the configuration object used by the manager's carriage-connect path.

The manager's carriage-connect method reads the four configuration properties in
this order: connect, receive-header, in-segment, and out-segment. It passes those
values unchanged, in that order, to the new carriage-agent constructor together
with the selected host and port, server type `3`, and secure-layer type `1`.
It then disables fallback on that agent, installs it in the manager, installs the
status handler, writes manager status `0x15`, and calls the agent's connect
method. The manager's `0x15` write is separate from later LocoAgent status
writes (`2` and `3`) observed in the connection callback; this contract does
not assign external names to those numeric values.

The fixture includes non-default fractional, zero, and negative inputs. The
traced manager path has no local positivity check before constructor forwarding,
so these cases require forwarding supplied values rather than hard-coding the
observed `15/20/10/10` tuple. A downstream timeout consumer may apply its own
admission policy; that is outside this startup constructor contract. The setter for the manager ping
interval stores its supplied value directly, and the LocoAgent receive-header
timeout setter stores its supplied value; those setters do not establish the
startup values in this contract.

The contract does not establish a universal default policy, all configuration
producers or wire decoding, reset/reconnect override behavior, queue execution
timing, secure-handshake success, or runtime socket completion behavior. Those
remain separate source questions. The fixture is a strict characterization of
observed startup ordering and is intentionally marked unexecuted at runtime.

## Static provenance

The observed booking initializer at `0x101404f54` loads floating-point constants
`15`, `20`, `10`, and `10` into `d0` through `d3` and calls the configuration
initializer at `0x1018d9ca0`. The manager carriage-connect implementation is
`0x1015182f4`. It reads the four timeout getters at `0x10151837c`,
`0x101518398`, `0x1015183b4`, and `0x1015183d0`, then calls the carriage-agent
constructor at `0x1015183f8`. The constructor call operands set server type `3`
and secure-layer type `1`; these are numeric wire/setup inputs, without external
labels assigned here. The subsequent calls disable fallback (`0x101518430`),
install the carriage agent (`0x10151843c`), install the status handler
(`0x101518508`), write manager status `0x15` (`0x10151851c`), and connect
(`0x101518534`).
