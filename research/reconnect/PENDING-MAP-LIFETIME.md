# Pending completion and packet-ID map lifetime

Status: reviewed static source supplement, 2026-10-02. This contract separates
normal packet response consumption from timeout/disconnect failure handling.

For an ordinary packet response, the consumer obtains the packet header's unique
ID and looks for a completion in the agent's pending-completion map. When a
completion matches, it removes that entry before invoking the completion with
`(packet, nil)`. When no completion matches, it forwards the packet to the
default receive handler when one is installed; with no default handler, the
reviewed body has no callback effect.

Packet production also maintains a packet-ID map whose key is an unsigned
packet ID and whose value is the request unique-ID string. Header completion
looks up that value, compares it directly with the incoming packet unique ID,
and only on equality derives the request tag and disables the corresponding
receive-header timeout. The reviewed path has no packet-ID-map removal. Its
lifetime across ordinary responses, disconnect fanout, and agent replacement is
therefore an explicit parity gap.

Disconnect fanout enumerates pending completions and sends each `(nil,
NSError(domain="LocoAgent", code=-1, userInfo=nil))`; the reviewed delegate does
not explicitly clear either map. A status handler or completion callback could
mutate state indirectly, so cleanup and queue-race policy remain separate
implementation decisions.
