# Regular-group outbound announcements and Boards mutations

Evidence date: 2026-10-10. Scope: Matrix room-topic changes in a bridged regular
group, whose topic mirrors the group's Boards announcement
([group announcements](GROUP-ANNOUNCEMENTS.md)), and other Boards mutations
from Matrix.

## Official-client contract (macOS 26.8.0, static, incomplete)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. Two bounded attempts
to finish the write-path trace stopped before completion, so everything here
is a lead, not a contract.

- Announcements are Boards (Moim) posts. The binary contains calls that create
  a post marked as an announcement, set the announcement to an existing post by
  post ID, remove it, and announce a chat message. They go to a separate Boards
  HTTP API rather than LOCO.
- The live implementation of these calls is in Swift, where request paths are
  built from inline literals. The request templates, methods, body fields,
  authentication headers, response model, error codes, permission checks (who
  may set or remove an announcement), retry behavior and the persistence and UI
  consumers of the result were **not traced**.

Recorded gaps: the complete Boards write chain (request, response, callers,
persistence, consumers and failure branches) and the server's permission rules
for regular groups. Without them mooo cannot send a Boards write whose outcome
it can classify, so it sends none.

## Production behavior and regression

Before this change, a Matrix topic change was ignored by the bridge framework
with a status that sent no notice, and the Matrix room kept the edited topic
although KakaoTalk was unchanged. The topic then disagreed with the
announcement until the announcement next changed, because the bridge's stored
topic already matched KakaoTalk. A failing production-path test preceded the
fix.

- A Matrix topic change (set, replace or clear) is rejected before any source
  request with a certain unsupported status and a notice telling the user to
  post or remove the announcement in KakaoTalk.
- The bridge bot restores the announcement topic in Matrix in the same handler.
  The stored portal topic is unchanged, so the next inbound announcement
  refresh still applies normally.
- Nothing is queued or retried. Each Matrix event gets one rejection, and a
  restart does not repeat it.
- Room name and avatar changes keep their existing explicit rejection (no
  restoration yet; see follow-ups). No other Boards mutation (posts, polls,
  comments) has a Matrix entry point.

## Owned encrypted acceptance

Method: a fresh build of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every native action, Matrix
topic change and bridge start/stop wrote a private EXCL 0600 receipt first; no
mutation was repeated. The topic was read from the local homeserver's room
state; messages with the tester device; the announcement from native A's
banner.

| Case | Result |
|---|---|
| A sent a synthetic text and announced it | topic set to the text; the announcement post bridged as a text post |
| Matrix user replaced the topic | one "not bridged" notice; topic restored to the announcement; native A banner unchanged |
| Matrix user cleared the topic | one notice; topic restored; native A banner unchanged |
| Restart | topic kept; no new notices, messages or topic events; delivery cursor equal to the latest mapping |

### Acceptance gaps

- No Boards write was attempted, so native permission checks and server
  refusal or ambiguous outcomes were not observed.
- The bridge bot's status notices in this encrypted room were unencrypted, as
  in earlier slices (framework behavior, not specific to this change).
