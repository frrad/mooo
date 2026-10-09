# Link (9) emitter and receiver audit

## Evidence and scope

Research date: 2026-10-09. Platform: owned Android lab emulators, KakaoTalk
26.8.2. Methods: scoped inspection of the authorized official APK using JADX
1.5.6 and one controlled A-to-B Android share. No public prior art was used.
Raw traffic, screenshots and decompiled output remain outside the repository.

Type 9 is named `Link` by the official message-type enum. Its receive model is
`LinkChatLog.kt` (runtime `com.kakao.talk.db.model.chatlog.l`, `classes8.dex`).
The attachment helper and spec are `LinkAttachmentHelper.kt` and
`LinkAttachmentSpec.kt` (runtime `t250` and `v250`, `classes11.dex`). These are
static observations, not executed payload parity.

## Controlled ordinary URL share

An Android `ACTION_SEND` with MIME `text/plain` and synthetic text
`https://example.com/mooo-synthetic-link` was routed through the official normal
share activity, `RecentExcludeIntentFilterActivity`. The sender selected one
owned recipient and pressed the one-recipient Send button once. The recipient
picker displayed the same recipient in recent-chat and friend sections; the
selected-recipient count was one.

The original owned secondary session captured the resulting `MSG`: its type was
**1**, with attachment keys `urls` and `f`. The owned primary receiver displayed
the synthetic URL in that chat. Confidence is high for this observed path.
Matrix delivery was not part of this emitter-classification experiment.

Ordinary URL sharing therefore does not supply a type-9 acceptance fixture.
This observation does not establish that every URL share uses type 1, or that
type 9 requires any particular SDK credential or registration arrangement.

## Static receiver observations

The type-9 chat-log model lazily parses KakaoLink attachments and derives its
text preview from that model. It exposes verification and forwarding flags;
parse failures leave those flags false. Unknown link-object classifications and
attachment parse exceptions also influence its unsupported-content predicate.

The attachment helper has multiple format branches:

- A nonblank link-version field delegates to another format parser.
- Otherwise an API-version classification selects an object-based format.
- One branch reads an `objs` array with application name, ID and version. When
  the attachment lacks `objs`, it attempts to parse the message string as JSON.
- Another branch reads objects, application metadata, action information and
  extras.
- A legacy metadata branch parses a JSON string stored in `metainfo` and derives
  text/action objects from the embedded metadata array.

Malformed required JSON becomes a KakaoLink parse exception. These observations
are leads for a complete implementation-neutral specification; the exact field
symbols, version mapping and object semantics still need tracing. No decompiled
implementation is copied into mooo.

## Implementation and acceptance gaps

Type 9 remains an explicit unsupported message. Do not enable a decoder from
the enum name, the URL-text experiment or the request constructor alone.

Before implementing it, capture a controlled type-9 message through an owned
official emitter and trace its request model, response, callers/callbacks,
persistent state, downstream consumers and failure behavior. The complete send
chain, remaining receiver object parsers, persistence and failure consumers are
currently untraced. Encrypted Matrix rendering, catch-up/live delivery, native
receiver comparison and restart behavior likewise remain unverified for type 9.
