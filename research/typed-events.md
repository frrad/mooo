# Typed incoming events

Status: implemented initial event layer, 2026-09-29.

## API contract

`Client.Events` converts the authenticated session's ordered unsolicited LOCO
packet stream into typed results. Repeated calls return the same channel. Raw
`Client.Pushes` and typed `Client.Events` are mutually exclusive for a client so
two consumers cannot silently split the ordered packet stream.

Each result contains either an event or a decoding error. A malformed known
packet produces a non-fatal error and decoding continues with the next packet.
Unknown LOCO methods and unsupported message types are explicit events rather
than failures. Their raw BSON is deliberately not exposed by the typed layer.

## Initial event types

- `TextMessage`: chat ID, log ID, optional author/time fields, and message text.
- `ReplyMessage`: reply text plus validated source log, author, type, optional
  link ID, and bounded source preview.
- `PhotoMessage`: the validated type-2 photo metadata used by the bounded media
  downloader.
- `UnsupportedMessage`: chat ID, log ID, and numeric message type.
- `ReactionChanged`: aggregate reaction items and the server revision from
  reaction metadata changes.
- `UnsupportedLogMeta`: chat ID, log ID, and unsupported metadata type.
- `UnknownPacket`: method name only.

Text, reply, photo, and reaction formatting redacts message, attachment, source,
and localized-label content so ordinary diagnostic formatting does not disclose
private payloads.

## Live validation

On 2026-09-29, the clean-room client connected from account B's persisted
Mac-class profile while the owned Android 26.8.2 account A sent a fresh
synthetic direct message. `Client.Events` emitted the exact payload as a
`TextMessage` without QR approval or reauthentication. The observed inbound
field is `chatLog.message`; outbound `WRITE` continues to use `msg`. No live
content or identifiers were retained. Confidence: high (controlled end-to-end
observation).

## Still pending

Read receipts, typing, chat/member changes, deletion, server changes, and
kickout need protocol-specific decoders before they graduate from unknown
events. Reaction aggregate changes are typed; reaction actor attribution is an
explicit HTTP lookup rather than part of the push event. Cursor persistence,
acknowledgement, deduplication, gap recovery, and offline catch-up remain
separate continuity work.

All automated fixtures are synthetic. Live message content and identifiers are
not stored in the repository.
