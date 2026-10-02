# Agent status gate for packet production

Status: reviewed static contract, runtime unexecuted.

The status read used by the packet-production block belongs to the `LocoAgent` owner. It is the agent's transport status, rather than a producer-local readiness field. The socket connect callback writes transport status `2`, then follows one of the observed secure-layer branches: no setup (including values outside the explicitly traced TLS and V2SL cases), TLS setup, or V2SL crypto construction plus a handshake write. The V2SL write uses timeout `-1` and tag `0`; this is a call argument, not a completion guarantee. It then writes transport status `3` immediately before starting the first header read. The reviewed body does not wait for a separate secure callback before those status-3/read-header calls; transport-level completion semantics remain a gap. The packet-production block reads the owner status when the queued producer work executes (the fixture names this input `execution_status`, distinct from the admission-time status), and admits allocation, send, and receive-header timeout arming only when that execution-time status equals `3`. A status transition after queueing therefore changes the branch; the queue execution race remains a gap.

A status other than `3` at execution time therefore takes the producer's failure branch: a supplied completion receives the producer error with no packet allocation, send, or receive-header timeout arm; without a completion, the branch has no callback effect. The status-3 send ordering and write callback behavior are specified separately in `PROTOCOL.md` and `WRITE-CALLBACKS.md`.

This establishes the source of the status input, not the external meaning of numeric status values. The relationship between the manager's status-change callback and the agent's status byte is also separate: manager-side status effects do not substitute for the agent status read used by packet production.

Evidence: RC-BIN-026 in `EVIDENCE.md`; synthetic vectors are in `internal/protocol/sessionlogin/testdata/reconnect/rc-q5-agent-status-gate.json`.
