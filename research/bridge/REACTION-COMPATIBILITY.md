# Reaction compatibility observed in A/B acceptance

Date: 2026-10-07, America/Los_Angeles. Reference client: owned Android
KakaoTalk 26.8.2, API 35; static control: authorized Mac 26.8.0 binary.
Method: fresh QR-enrolled B bridge, synthetic A/B direct-room messages, single
Matrix mutations, official phone UI, private bounded HTTP-response and LOCO
metadata capture. Account identifiers and raw captures are excluded.

## Observed behavior

- The legacy mutation endpoint returned HTTP 200 with `{"result":true}` and
  the requested reaction appeared on the phone. Requiring `status` made the
  bridge report an unknown outcome despite successful mutation. Accept a true
  boolean result or the previously observed numeric status zero. A nonzero
  status or false result rejects; absent, null, mistyped, or trailing JSON
  cannot establish success. Never retry the earlier ambiguous mutations.
- Legacy changes arrived as type-1 `CHGLOGMETA`, with selection-keyed counts
  such as `{"2":1}`, positive revision, nullable direct-chat `linkId`, and
  change-specific `extra` data. Decode this separately from type-2 `rx` item
  aggregates. Continue using `/members` for complete actor attribution;
  `extra.userId` is not a complete roster.
- Android's current quick-reaction picker produced type-2, kind-2 mini items
  for heart and thumb. One actor could hold both at once. Their detail endpoint
  returned `status:0` and kind/item-ID/string-user-ID records; the observed
  records had localized `a` labels without `itemMeta`. Aggregate labels supply
  Matrix text, with stable `kakao:mini:` IDs distinguishing custom items.
- A mini-only message's legacy lookup returned exactly `{"revision":0}`.
  That is an empty legacy roster, not a failed mini lookup. It does not relax
  positive-revision requirements for populated legacy rosters.
- Tapping an already-selected quick-menu item did not remove it. Tapping its
  owned reaction bubble removed it. Matrix redaction and stored-row removal
  were observed, including removal of the final mini item.

## Implementation decisions and limits

Mini attribution must match the triggering aggregate's item IDs and exact
distinct actor counts before replacing state. Unknown kinds, malformed actor
identity, mismatched snapshots, and lookup failures stop synchronization without
advancing the checkpoint. The bridge checks both the legacy and mini lookup;
legacy-only pushes preserve existing mini rows. Mini and legacy revisions are
persisted separately, and failed Matrix operations do not advance them.

Localized mini labels are sent as Matrix reaction text; proprietary images are
not copied. Sending new mini/custom reactions from Matrix remains unsupported;
outbound support remains the six explicit legacy selections and cancellation.
The compatibility fix does not establish general reaction parity or group-room
behavior.

## Official-client chain audit

The version-matched Mac static trace confirms separate old/mini metadata paths,
revision checks, update dispatch, and a revision-guarded old-metadata setter.
The mutation wrapper delegates to its existing Swift request implementation;
earlier request/member/detail dossiers remain in
[`../replies-and-reactions.md`](../replies-and-reactions.md).

Untraced layers remain explicit: the complete Swift boolean-result callback,
type-1 response-model constructor, dispatched persistence blocks, and every
downstream UI/failure consumer. Ghidra merged adjacent default/mini function
boundaries, so its combined decompilation is not an executable oracle. Live
observations support these specific compatibility paths, not full official
persistence or failure parity. No public prior art was used.
