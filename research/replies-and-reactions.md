# Replies and reactions

Status: implemented and live-validated against owned disposable accounts,
2026-09-29.

## Provenance and method

This contract was derived first from the authorized KakaoTalk macOS 26.8.0
binary and controlled traffic between the clean-room Go client and an owned
KakaoTalk Android 26.8.2 account. Public prior art was not used to choose the
wire shapes. It may be used afterward as a consistency check under the
repository's clean-room policy.

Static analysis used selectors, constant strings, and decompiled control flow;
no decompiled source or proprietary assets are included here. Live experiments
used synthetic message text and retained no account identifiers, credentials,
private content, screenshots, or packet captures.

## Replies

A reply is the existing LOCO `WRITE` operation with message type `26`. The
ordinary reply text remains in `msg`; `extra` is a JSON string containing:

| Field | Meaning |
| --- | --- |
| `src_logId` | Source message log ID |
| `src_userId` | Source author user ID |
| `src_linkId` | Open-chat link ID; omitted for an ordinary direct chat |
| `src_type` | Source message type |
| `src_message` | Bounded source-message preview |
| `src_spoilers` | Empty array for the observed ordinary-text case |

The source preview is limited to 100 UTF-16 code units, matching the official
client's `NSString` behavior. The implementation refuses to split a surrogate
pair at the boundary. Sending remains an exactly-once mutation: an ambiguous
transport failure is returned to the caller and is never retried implicitly.

An official Android reply to a synthetic Go-client message arrived as a type-26
`MSG`, and the source log ID, author ID, type, and preview matched. A subsequent
Go-client reply rendered as a quoted reply card in the official Android UI.
An ASCII source longer than the limit was observed as exactly 100 characters
without an ellipsis. Confidence: high for direct-chat text replies; open-chat
and attachment-specific reply fields remain unvalidated live.

## Reaction mutation

The macOS client uses authenticated HTTPS rather than LOCO for a reaction:

```text
POST https://talk-pilsner.kakao.com/messaging/chats/{chatId}/bubble/reactions
```

The JSON request contains `logId`, `type`, and a millisecond Unix `reqId`.
`linkId` is included for an open chat and omitted for an ordinary direct chat.
The recovered selection values are:

| Value | Selection |
| ---: | --- |
| 0 | cancel the caller's reaction |
| 1 | heart |
| 2 | like |
| 3 | check |
| 4 | laugh |
| 5 | surprise |
| 6 | sad |

The request uses the persisted Mac-class access token and device UUID, so it
does not create or reconnect a LOCO session. The client performs the mutation
once and accepts only a successful HTTP response whose JSON status is zero.
A production Go-client heart mutation returned status zero and appeared on the
official Android message bubble. Confidence: high for heart and cancellation's
request contract; medium for the remaining recovered selection labels until
each is exercised live.

## Incoming aggregate and member attribution

Reaction changes arrive over the existing LOCO session as `CHGLOGMETA`. Metadata
type `2` has a JSON `content` string with an `rx` array. Each aggregate entry
contains localized label map `a`, count `c`, kind `k`, and stable item ID `o`.
The outer packet supplies the chat ID, target log ID, and revision. This event is
aggregate state; it does not identify the actors.

Actor attribution is a separate authenticated read:

```text
GET https://talk-pilsner.kakao.com/messaging/chats/{chatId}/bubble/reactions/{logId}/members
```

Its JSON object maps string selection values (`"1"` through `"6"`) to arrays of
user IDs and includes a positive `revision`. The aggregate item's `o` identifier
is therefore not interchangeable with the outbound selection value or member-map
key.

An official Android heart produced a type-2 `CHGLOGMETA` with count one. The
production Go client then sent a synthetic message, added its own heart, fetched
members through the public client method, and received one heart member matching
the authenticated disposable account with a positive revision. Confidence: high
for the observed direct-chat heart path and response shape.

## Implementation boundaries

- `internal/protocol/chat` owns type-26 reply serialization.
- `internal/protocol/reactions` owns bounded HTTP request/response codecs.
- `internal/protocol/events` exposes typed reply and aggregate-reaction events
  without including raw payloads in diagnostic formatting.
- `internal/client` integrates both paths with the persisted profile and the
  existing single-session lifecycle.

Automated tests use synthetic identifiers and content. The remaining work is to
observe all selection types, reply-to-media variants, open-chat `linkId` behavior,
and any reaction revision conflict semantics.
