# NOTIREAD response and completion boundary

Status: first-party static trace, macOS KakaoTalk 26.8.0, reviewed
2026-09-30.

This note extends the inbound `NOTIREAD` request dossier with the response,
completion, and failure layers recovered from the authorized official Mac
client. It contains no decompiler output, addresses, account values, message
contents, or proprietary assets.

## Observed response path

The request-specific completion block receives either no packet or a non-null
packet from the shared carriage layer. With no packet it invokes the supplied
completion with a nil response. With a packet it constructs the official
`NOTIREAD` response model from that packet and forwards the model to the
supplied completion. The block does not inspect a status field before making
that choice.

The response model has a `notiRead` boolean accessor. The accessor reads a
boolean-like value from the packet's optional extra-information dictionary and
returns true when that value is absent. Static evidence does not establish the
exact dictionary key's server meaning, nor does it establish that this
property is an acknowledgement-success predicate.

At the shared carriage layer, a pending request is indexed by the allocated
packet identity and tag. When a matching response packet arrives, the pending
callback is removed and invoked with the packet and a nil error. The reviewed
callback path has no response-status conditional. Unsolicited packets instead
go through the default method-to-response dispatcher; no dedicated
`NOTIREAD` state consumer or handler was found in the reviewed selector set.

## Failure and disconnect behavior

The shared sender checks carriage readiness before queuing a request. When the
agent is unavailable or not in its send-ready internal state, it invokes the
completion with no response and an error. A ready request allocates one packet
identity, stores one completion, sends one packet, and arms one receive-header
timeout; the traced path contains no retry loop.

If the receive-header timeout fires, the carriage agent is disconnected. The
shared carriage trace establishes that socket close, receive timeout, and
explicit disconnect fan out the supplied error to every pending callback,
clear the pending maps, and terminate each callback exactly once. This is a
generic carriage guarantee rather than a NOTIREAD-specific persistence write
or status conversion. A timeout or disconnect is therefore still an
ambiguous, unacknowledged mutation and must not be converted into local read
success or retried implicitly by a clean-room client.

No NOTIREAD-specific caller-side persistence, application-message commit, or
downstream follow-up was found after the completion boundary. Inbound message
delivery remains a separate operation from this automatic acknowledgement.

## Conformance consequence

The synthetic response test in
`internal/protocol/notiread/notiread_test.go` uses a response with a negative
status and an optional notification-read value, then asserts that the one-shot
transport returns the response bytes unchanged. This is a characterization
test: the current transport boundary already passes, but the test protects
the first-party-observed absence of a status interpretation at this layer.
It intentionally does not invent a success predicate, response-key contract,
or post-disconnect callback result.

## Provenance and confidence

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method: exact selector lookup, callback-block tracing, response initializer
  and accessor tracing, pending-response dispatch tracing, shared carriage
  readiness/send/timeout tracing, and disconnect-path inspection.
- Confidence: high for packet forwarding, one-shot pending-callback removal,
  shared pending-callback fan-out after timeout/disconnect, and the absence
  of a status branch on the reviewed path; medium for the response property's
  optional extra-information interpretation; open for server semantics of the
  response boolean.
