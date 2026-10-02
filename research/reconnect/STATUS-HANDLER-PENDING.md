# Status handler and pending-completion boundary

Status: reviewed static source supplement, 2026-10-02. This contract scopes the
manager's installed status handler and the socket disconnect fanout that follows
receive-timeout failure. It does not claim disconnect cleanup of the pending map or queue timing.

`setStatus:error:` writes the agent's status byte from the supplied status and,
when a status-change handler is installed, synchronously invokes that handler
with `(agent, oldStatus, newStatus, error)`. Same-status writes still invoke the
handler. With no installed handler, the setter has no callback effect.

The manager handler first compares the callback agent against the manager's
current carriage agent. A stale-agent callback has no effects. For status `0`,
it clears the manager's carriage-agent slot, writes internal status `0x1A` when
the handler latch was already set or `0x16` otherwise, invokes the supplied
boolean callback with `false` only while the latch is clear, queues cancellation
of the manager PING selector on the main queue, and clears the status-change
handler. For status `3`, it writes internal status `0x17`, sets the per-handler
latch only when clear, and invokes the supplied callback with `true` only on
that transition. The latch is initialized clear when the handler is installed;
it is not an ongoing activity flag. Numeric status labels remain untraced. The reviewed manager installer supplies a callback block, but whether a nil boolean callback is a supported installation state is not proven; vectors therefore make callback presence explicit.

After a socket disconnect, the delegate calls `setStatus:error:` first. The
optional status handler therefore runs before delayed-work cancellation and
pending-map enumeration. The delegate then cancels delayed work for the agent
target and enumerates pending completions. Each completion receives
`(nil, NSError(domain="LocoAgent", code=-1, userInfo=nil))`. The reviewed
handler does not explicitly clear the pending map. Whether the installed
status handler, completion callback, or another owner later removes entries is
untraced. The manager status handler itself has no pending-map access.

For an ordinary packet response, the producer consumer gets the packet header unique ID, then looks it up in the pending-completion map. A match removes that completion entry before invoking it with `(packet, nil)`. A miss routes the packet to the default receive handler when one exists; with no default handler, that fallback has no callback effect in the reviewed body.

The producer's packet-send block carries a weak send/status object and a
separate strong timeout-scheduling object. The reviewed block does not compare
them, and its creator does not expose enough source to prove they always
refer to one instance. An independent implementation must preserve this as an
identity gap rather than silently collapsing the captures.

Implementation decisions are to expose ordered status/fanout effects, make
handler absence explicit, and choose pending-map cleanup and queue-race policy
separately. These decisions do not claim official cleanup behavior.
