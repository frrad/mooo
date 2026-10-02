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

The fixture includes a second input with non-default fractional seconds. Its
purpose is to require forwarding of supplied configuration values rather than
hard-coding the observed `15/20/10/10` tuple. The setter for the manager ping
interval stores its supplied value directly, and the LocoAgent receive-header
timeout setter stores its supplied value; those setters do not establish the
startup values in this contract.

The contract does not establish a universal default policy, all configuration
producers or wire decoding, reset/reconnect override behavior, queue execution
timing, secure-handshake success, or runtime socket completion behavior. Those
remain separate source questions. The fixture is a strict characterization of
observed startup ordering and is intentionally marked unexecuted at runtime.
