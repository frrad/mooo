# Direct messaging validation

This record contains sanitized evidence from an operator-owned disposable A/B
direct room. It does not establish general Kakao or Matrix parity.

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
mutation; this does not establish a product missing-target failure. The one
heart reaction mutation and reported a generic failure. Retained evidence
cannot distinguish server rejection from transport or another ambiguous
outcome; neither operation was retried.

After a clean stop, the same profile and database were restarted once. The
bridge reached `CONNECTED` again with no duplicate backfill; the database had
eight message rows with eight distinct IDs. The container was then stopped
cleanly. This validates one persistent resume cycle and the specific media
path, while leaving reactions, replies, network-failure recovery, and broader
parity unresolved.
