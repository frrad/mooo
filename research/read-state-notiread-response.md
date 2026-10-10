# NOTIREAD response and completion boundary

Status: first-party static trace, macOS KakaoTalk 26.8.0, reviewed
2026-09-30; response consumer corrected 2026-10-10.

This note extends the inbound `NOTIREAD` request dossier with the response,
completion, and failure layers recovered from the authorized official Mac
client. It contains no decompiler output, addresses, account values, message
contents, or proprietary assets.

## Observed response path

Correction (2026-10-10): the only `NOTIREAD` send passes no completion
handler, so no code reads the response. The response class has no methods of
its own. An earlier version of this note attributed to the response a
`notiRead` accessor that reads extra information and defaults to true. That
accessor belongs to the room model; see the
[request dossier](read-state-notiread.md#the-room-notiread-flag).

A request-specific completion block wraps a non-null packet in the response
model and passes a nil packet through as nil, without checking status. With
the nil completion used by the only caller, nothing runs after the packet
arrives.

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

A synthetic response test formerly in
`internal/protocol/notiread/notiread_test.go` (deleted 2026-10-05 with the
unused model) characterized this boundary: a response with a negative status
and an optional notification-read value passed through the one-shot transport
unchanged. The official client ignores the response entirely. A future sender
should do the same: fire and forget, without inventing a success predicate, a
response-key contract or a post-disconnect callback result.

## Provenance and confidence

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method: exact selector lookup, callback-block tracing, response initializer
  and accessor tracing, pending-response dispatch tracing, shared carriage
  readiness/send/timeout tracing, and disconnect-path inspection.
- Confidence: high for packet forwarding, one-shot pending-callback removal,
  shared pending-callback fan-out after timeout or disconnect, the absence of
  a status branch, and the nil completion of the only caller; open for any
  server meaning of the response.
