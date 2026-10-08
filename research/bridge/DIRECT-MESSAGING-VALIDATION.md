# Direct messaging validation

This record contains sanitized evidence from an operator-owned disposable A/B
direct room. It does not establish general Kakao or Matrix parity.

## Other-participant A/B acceptance — 2026-10-07

Source baseline `c464ca0`, followed by the reaction compatibility fixes in this
change. Owned Android clients: KakaoTalk 26.8.2, API 35. B used the fresh native
QR profile from [QR enrollment](QR-ENROLLMENT.md). A remained an independent
primary participant; no secondary login on restricted A was retried. The local
Synapse portal was unencrypted. Evidence came from exact synthetic fixture
matches, official phone UI, Matrix events, source IDs and the bridge database.

Passed paths:

- A participant text created one direct portal and arrived once as A's ghost.
  The ghost and room name matched A, and membership included both participants.
- Matrix text appeared on A's phone and persisted with positive Kakao source
  IDs. B's phone also displayed the independent A text.
- Matrix-to-Kakao PNG rendered on A. An original-quality 64x64 PNG sent by A
  arrived once as A's Matrix image, with byte-for-byte matching media content.
- A phone reply referenced the exact Matrix source event. A Matrix reply after
  restart rendered the persisted original message in the same Kakao reply bubble
  and persisted as type 26.
- Two A messages sent during a clean bridge stop recovered once each, in order,
  before a fresh live message, using the same database/profile and no new QR.
- A private current-source harness closed only B's active carriage while idle,
  gated its replacement, and sent one A message during that gap. Automatic
  recovery delivered it once before a later live fixture. Production bootstrap,
  profile lease, continuity and supervisor paths remained in use. This proves
  this one controlled drop, not every terminal event or timeout path.
- Reaction acceptance exposed and fixed boolean mutation responses, legacy
  type-1 pushes, and current mini attribution. Fresh outbound legacy addition
  and cancellation persisted successfully. A's two distinct mini items arrived
  with correct attribution; individual and final removal redacted Matrix events
  and deleted stored rows. See [compatibility evidence](REACTION-COMPATIBILITY.md).

The original failed mutations were never resent automatically. Fresh targets
and explicitly chosen state changes tested fixes. Transient tooling captures
stayed outside the repository. Group testing is pending at the maintainer's
direction because the lab has only two owned participants. Avatars beyond the
default profile, room E2EE, receipt/SYNCMSG read-side effects, membership changes,
terminal events, cleanup timeouts, and Matrix delivery faults remain separate.

## Native bridge smoke

On 2026-10-04, the native bridge at source revision `a8d8970` was exercised
with the owned tester and an imported secondary-device profile. The method used
the Matrix portal invitation/join flow, phone UI observation, Matrix event
inspection, and a clean bridge shutdown. The observed path was:

- a phone own-device message arrived in Matrix and created one direct portal
  with one stored message;
- Matrix text sent back to Kakao arrived in the phone conversation;
- a Matrix reply to a persisted phone-originated message appeared in Kakao with
  the original Kakao message context;
- a phone-originated reply to Matrix text arrived as a Matrix event; HTTP 200
  inspection confirmed `m.relates_to.m.in_reply_to` targeted the known Matrix
  event. The phone UI itself does not expose the Matrix event ID;
- one Matrix-to-Kakao PNG completed as a rendered photo bubble, rather than a
  loading placeholder;
- the final private database observation had one login, one portal, and five
  messages, with no duplicate rows observed; shutdown completed cleanly.

The inbound path above is specifically the logged-in device's own-device
delivery. It does not prove delivery from another Kakao participant. The photo
observation covers both directions: the 2026-10-05 (UTC) follow-up at source
revision `a8d8970` verified one Kakao-to-Matrix 64x64 PNG with matching payload
hash and one completed Matrix event, also through the logged-in own-device
path.

## Explicit gaps

This run did not prove group rooms, encrypted rooms, reactions, restart/offline
recovery, or fresh QR enrollment. It did not prove
the full alpha acceptance sequence. The QR follow-up on 2026-10-05 (UTC) used source revision
`324ad37caa52a3a56960c60acd8b63dc4b247220` (the PR183 head, rather than current
main), a fresh profile, and exercised bounded expiry and profile cleanup after
an invalid input selection. Android failed the scan before the QR-info lookup,
so renderer comparison remained unknown; approval and a server-side cause were
not established.

## Container resume

Separately, image source revision `af1a0d9` built as a Linux arm64 Docker image
with Go 1.27.1, cgo SQLite, and `goolm`. Container help, version JSON, example
configuration, registration generation, compose configuration, and persistent
path/UID checks passed with external temporary data. During those initial
static checks, no Kakao session was started from that image. These checks
establish packaging and static configuration only, not live bridge messaging
or recovery.

On 2026-10-05 (UTC), image digest
`sha256:2a1a64393d77d216e65a586b76a188a3cb5755c7ded6ff632a84b860a180e203`
was resumed against the existing owned profile and a stopped bridge database.
The container reached `CONNECTED`
and the homeserver/appservice ping returned HTTP 200. One Matrix text was
accepted and persisted. One prepared encrypted Matrix attachment in the
unencrypted portal was downloaded, decrypted, and delivered as a Kakao photo;
the resulting source event had a positive log ID and persisted photo metadata.
The one reply attempt used a synthetic target whose Matrix event ID was
newline-contaminated by the test harness, so it was rejected before Kakao
mutation; this does not establish a product missing-target failure. The
connector attempted one heart reaction mutation and reported a generic failure.
Retained evidence
cannot distinguish server rejection from transport or another ambiguous
outcome; neither operation was retried.

After a clean stop, the same profile and database were restarted once. The
bridge reached `CONNECTED` again with no duplicate backfill; the database had
eight message rows with eight distinct IDs. The container was then stopped
cleanly. This validates one persistent resume cycle and the specific media
path, while leaving reactions, replies, network-failure recovery, and broader
parity unresolved.
