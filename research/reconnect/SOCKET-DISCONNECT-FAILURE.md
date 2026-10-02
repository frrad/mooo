# Socket disconnect failure contract

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

The LocoAgent socket-disconnect callback receives the agent, socket, and error.
In the reviewed continuation it first calls `setStatus:error:` with agent status
`0` and the socket error. If a status handler is installed, that setter invokes
the handler synchronously before the continuation proceeds. The continuation
then cancels delayed work using the agent as the target, enumerates the agent's
pending-completion map, and invokes each completion with a nil result and the
reviewed `NSError` shape `domain="LocoAgent"`, code `-1`, and nil user info.
An empty pending map produces no completion calls.

The callback does not show a direct clear of either pending map. The installed
status handler can have indirect effects before enumeration, but its mutation of
pending state is not established here. Therefore a clean implementation must
preserve the observed fanout and ordering without assuming that this socket
path clears its maps.

A separate `failPendingRequestsWithError:` helper has a stronger, distinct
contract. It enumerates the completion map with the supplied error, then calls
`removeAllObjects` on the completion map and the packet-ID/unique-ID map, in
that order. Its recognized caller is a separate Swift/manager disconnect path;
that helper behavior must not be substituted for the LocoAgent socket callback.

## Static provenance

The socket callback continuation is within the LocoAgent callback beginning at
`0x101774348`; the disconnect branch's reviewed calls are `setStatus:error:` at
`0x101774910`, delayed-work cancellation at `0x101774920`, and pending-map
enumeration at `0x101774930`. The pending callback block is supplied to that
enumeration call and performs the nil-plus-error completion fanout.

The separate helper is `failPendingRequestsWithError:` at `0x101774ce8`. It
enumerates `self+0x28`, then clears `self+0x28` and `self+0x30`. Its recognized
caller is disconnect IMP `0x100d451ac`, which delegates to a Swift work-item
helper at `0x100d44f40`. These addresses identify this client build and are
provenance, not stable API identifiers.

The fixture is a strict characterization of source ordering and map ownership;
runtime queue execution, handler-induced mutation, socket-path map clearing,
and durable request retry policy remain unexecuted or untraced.
