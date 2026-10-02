# Reconnect initial carriage lifecycle

Status: static source supplement, 2026-10-02. This dossier records the
initial carriage setup boundary and does not assign external names to internal
status values.

The manager's carriage-connect method constructs a new carriage-agent
instance from the selected host, port, server mode, secure-layer mode, and
the four timeout values. It disables fallback on that newly initialized
agent, then installs the agent in the manager's carriage-agent slot. It obtains
the default receive handler and installs a status-change callback with a
zero-initialized by-reference latch. It writes
internal status `0x15` and then calls the carriage `connect` method.

That connect method body contains no direct delayed PING scheduling. Interval
scheduling is proven in ordinary request, request-completion, and
fire-and-forget push-receipt paths; the ordering of the first such path relative
to completion of carriage setup remains untraced. The request and push-receipt
paths queue cancellation, perform their send inline, and queue the interval
scheduling callback. The request-completion path queues interval scheduling
before forwarding packet/error arguments to its supplied callback.

The status callback compares its incoming carriage-agent argument with the
manager's current carriage agent before applying its numeric branches. This
prevents stale-agent callbacks from mutating the current session. The
per-handler latch is initialized clear when the handler is installed; the
success branch sets it once, while the false branch does not set it. It is not
an ongoing activity measurement.

Remaining gaps are the external meanings of internal statuses `0x15`, `0x16`,
`0x17`, and `0x1A`, and any additional scheduler admission outside the traced
request and push-receipt paths.
