# Direct messaging validation

This record contains sanitized evidence from an operator-owned disposable A/B
direct room. It does not establish general Kakao or Matrix parity.

## Native bridge smoke

On 2026-10-04, the native bridge at source revision `a8d8970` was exercised
with the owned tester and an imported secondary-device profile. The method used
the Matrix management-room invitation/join flow, phone UI observation, Matrix
event inspection, and a clean bridge shutdown. The observed path was:

- a phone own-device message arrived in Matrix and created one direct portal
  with one stored message;
- Matrix text sent back to Kakao arrived in the phone conversation;
- an inbound Kakao text was replied to from Matrix, and the phone bubble showed
  the expected reply relation to the known Matrix event;
- a Matrix reply to a Kakao message arrived in the phone conversation;
- one Matrix-to-Kakao PNG completed as a rendered photo bubble, rather than a
  loading placeholder;
- the final private database observation had one login, one portal, and five
  messages, with no duplicate rows observed; shutdown completed cleanly.

The inbound path above is specifically the logged-in device's own-device
delivery. It does not prove delivery from another Kakao participant. The photo
observation covers Matrix-to-Kakao only.

## Explicit gaps

This run did not prove group rooms, encrypted rooms, Kakao-to-Matrix photos,
reactions, restart/offline recovery, or fresh QR enrollment. It did not prove
the full alpha acceptance sequence. The current QR follow-up on 2026-10-05
used a fresh profile and exercised bounded expiry and profile cleanup after an
invalid input selection; it did not establish renderer lookup, approval, or a
server-side cause.

## Container smoke

Separately, image source revision `af1a0d9` built as a Linux arm64 Docker image
with Go 1.27.1, cgo SQLite, and `goolm`. Container help, version JSON, example
configuration, registration generation, compose configuration, and persistent
path/UID checks passed with external temporary data. No Kakao session was
started from that image. These checks establish packaging and static
configuration only, not live bridge messaging or recovery.
