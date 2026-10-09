# Kakao location-card observations

2026-10-09, authorized owned Android KakaoTalk 26.8.2 A/B experiment; no
public prior art consulted. A synthetic emulator GPS fix was set at a public
landmark (40.7484, -73.9857). The first Location picker still showed Android's
default Mountain View location. Reapplying the GPS fix after permission approval
and tapping my-location selected New York West 34th Street 20. A durable private
receipt preceded tapping the exact address bubble, which sent immediately.
A scoped B MSG capture observed type 16.

## Observed fields and official model

`lat` and `lng` were JSON numbers, `a` the address, `t` an empty title and `c`
boolean true. Tiny coordinate differences from the requested GPS fix occurred;
preserve received values instead of asserting rounded injection values.

Authorized DEX source-file mapping identifies LocationAttachment.kt as
com.kakao.talk.bubble.location.LocationAttachment in classes8.dex and
LocationChatLog.kt as com.kakao.talk.db.model.chatlog.m. Single-class JADX
inspection maps lat/lng to coordinates, a/t to address/title, c to isCurrent,
and optional cid to a separate identifier. The serializer omits cid at its
invalid sentinel (-1), and omits NaN coordinates. This is a static lead; omitted
coordinates are not evidence for a valid deliverable location.
The model's text formatter selects the address when title is blank, otherwise
title plus address, and optionally appends a region-dependent map link.
Parcelable state stores coordinates, address, title, current flag and cid.

## Implementation scope and gaps

Use native Matrix m.location with a geo URI containing received coordinates.
The observed current-location flag does not prove continuous/live-location
updates. Do not send physical-device coordinates, automatically open map links,
or fabricate preview images. Trace request/response, share callbacks, chat-log
initialization/persistence, downstream card opening and failure behavior.
Those layers, OldLocation (10), named place/cid variants remain gaps; the observed fixture is a production-test input, not broad parity.

## Local implementation and encrypted acceptance

The production decoder requires finite numeric coordinates within geographic
ranges and preserves received precision. Missing/null/string coordinates and
out-of-range values produce an explicit identifiable delivery gap. Names and
JSON objects are bounded. Matrix receives native `m.location` with a `geo:` URI
and readable address/title; no map thumbnail is invented. The bridge text policy
uses title then address on separate lines, differing deliberately from the
Android title/address/map-link formatter.

Catch-up and a fresh shared-script live delivery passed encrypted SDK decryption
with expected message type, address and exact received geo URI. The second
share's coordinates differed slightly from the first; reusing the first exact
expectation failed. A scoped read of the second original log established its
coordinates independently, and those exact values matched the Matrix event.
After a normal restart, the database retained exactly two location identities.
The owned B client displayed both cards and opening the fresh card showed the
intended landmark and address in Kakao's View Location screen. The screen was
closed without using its current-device-position or external map-app actions.
Earlier encrypted events still decrypted with retained keys.

The official chat-log initializer parses nonblank attachment JSON into the
location model; a JSON exception leaves initialization unset. The model reads
coordinates/address/title/current/cid with optional JSON getters. The bridge
requires actual finite coordinates rather than forwarding optional getter NaN
results. Shared request/callback chains and full persistent state consumers
remain explicit gaps.

## Repeatable owned-emulator sender

`research/emu.sh send-location a` consumes private JSON with `peer`, `latitude`,
`longitude`, exact expected `address`, and an owner-only absolute `receipt` path.
It requires an owned selected chat and empty composer, opens Location, reapplies
synthetic GPS through the emulator console, taps my-location, then waits through
at most four UI snapshots for the exact address control inside the send bubble.
Recentring briefly removes those controls; unknown permissions require manual
inspection. Android exposes the address as a sibling in the accessibility tree,
so the guard uses control identity and geometric containment. Regression tests
cover the observed loading/flattened-layout failures. The helper reserves the
receipt before the bubble tap, which sends immediately, and never retries a send.
