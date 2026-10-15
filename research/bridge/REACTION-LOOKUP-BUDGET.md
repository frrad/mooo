# Reaction attribution reads: derivation, coalescing and budget

Status: implemented 2026-10-15 for [native skew audit](NATIVE-SKEW-AUDIT.md)
finding #4. Static evidence is from the authorized official macOS KakaoTalk
26.8.0 arm64 binary, read-only, in the private Ghidra project. Owned live
acceptance is recorded below.

## Official-client contract (static, 2026-10-15)

Method: read-only headless queries and parity reports on the Mac binary. No
public prior art was used. The private reports stay under the external lab
root.

- **One storage path for push and sync.** The `CHGLOGMETA` push handler looks
  up the stored meta by chat, log, link and type, and creates it when it is
  missing. A legacy (type 1) push with a higher revision replaces the stored
  `revision`, `content` and `extra` as they arrive. It also raises the room's
  `lastLogMetaRevision` and clears the message's reaction cache. Sync-meta
  rows use the same fields and the same higher-revision rule. Confidence:
  high for the push, medium for the full field list the sync block writes.
- **`extra` is read only for `my`.** The legacy item builder decodes `extra`
  and reads `my` as an integer. The item whose selection equals `my` is
  marked as the viewer's own reaction. No code reads `extra.userId`, and the
  builder never compares an ID with the logged-in user. No separate own-pick
  field is stored. Confidence: high.
- **Counts replace the aggregate.** Items are rebuilt from `content` keys
  `"1"` to `"6"` with positive counts. The client derives and stores no
  per-actor delta. Confidence: high.
- **Own reaction POST.** On success the client forwards the response and
  changes no reaction state. The own pick changes when the next push or
  sync-meta row arrives. Tapping one's own reaction sends selection 0
  (cancel). Confidence: medium-high; the generic HTTP response decoder is
  untraced.
- **Attribution reads.** `GET …/bubble/reactions/{logId}/members` and
  `POST /emoticon/chat/rx/log-details` have one caller: the click handler of
  the reaction detail view. It requests members when the message has a legacy
  reaction and log details when it has a Mini reaction. No other direct or
  selector call site exists. Confidence: high. Dynamic dispatch cannot be
  ruled out.

Owned observation (2026-10-15): legacy pushes that this account's own
reactions caused carry `extra` with `my`, `userId` (this account) and a
numeric `type`. `type` was recorded only as a number, but it matched `my` on
all three pushes: the bridge's self-consistency check passed every time
without a fallback read. Mini (type 2) pushes carry `extra: null`
([fixture](../fixtures/reactions/observed-own-legacy-pushes.json)).

Hypothesis, not yet observed: native applies `extra.my` without checking who
acted, so its display is right only if the server writes `extra` separately
for each recipient, pushes included. Another member's legacy reaction would
settle it. The lab has no second legacy sender: Android's quick reactions are
Mini items. Until it is observed, a push carrying another member's `userId`
is still checked against the counts, so a different meaning falls back to a
read.

Untraced: how the push notice decodes its BSON body (it is dispatched through
a table), Swift readers of `extra` that use inline literals, and the Mini
`extraRevision` path.

## Bridge policy

Matrix shows one reaction event per actor. Native shows only counts and the
viewer's own pick, so the bridge needs actors that native never asks for. The
bridge gets them in this order:

1. **Derive without a read.** For a legacy push or sync-meta row, the bridge
   starts from the stored legacy roster. It applies `extra.my` as this
   account's own pick. If `extra.userId` and `extra.type` are present, it
   applies them as one actor's new selection; native ignores both fields.
   Without an actor, it can attribute one more change: a selection whose
   count dropped by one and that only one other member held, optionally
   reappearing on one other selection. The derived roster must reproduce the
   push's counts exactly. A derivation also needs the stored roster to have
   been established on the current session, by a read or an earlier
   derivation, at exactly the stored revision. Changes made while the bridge
   was offline therefore never feed a derivation.
2. **Coalesce reads per message.** A live push that needs a read reads at
   once. Further pushes for the same message within 2 seconds share one
   trailing read of the newest revision.
3. **Account budget.** Every members or log-details read draws on one
   in-memory token bucket per login: 10 reads at once, refilled at one per
   20 seconds. A Mini change costs two reads. A live push over budget is
   consumed without a failure notice or a persisted revision. A viewing
   resync over budget stops with its cursor retained (`errReactionSyncBudget`)
   and continues on a later tick. Changes skipped this way come back through
   viewing resync, or through the next push for the message.

### Deliberate deviation

**Remaining reads.** These requests still go to `/members` (and log details
for Mini), which native sends only from the detail view:

- a live `CHGLOGMETA` push that derivation cannot explain: an addition by
  another member without `extra.userId`/`extra.type`, inconsistent counts,
  no `extra.my`, or the first push for a message on this session;
- a viewing-resync row with the same limits;
- every Mini (type 2) change.

The trigger is a reaction change Matrix must attribute. The 2-second window
and the bucket size are bridge policy, not traced native intervals.

## Owned acceptance (2026-10-15)

B bridge with a call-counting overlay, owned A/B/C group, one bridge session:

| Action | Requests |
|---|---|
| Matrix ❤️ on a message (first push for it this session) | 1 reaction POST, 1 `/members` |
| Matrix 👍 replacing it | 1 reaction POST, no `/members` (derived from `extra.my`) |
| Matrix redaction of 👍 | 1 cancel POST, no `/members` (see #5), push derived |
| A's quick heart (Mini), then its removal | 1 `/members` + 1 log details each |

Before this change the same sequence read `/members` six times (including the
cancel pre-read) and log details twice. Now it reads `/members` three times
and log details twice. All the remaining reads are first or Mini changes.

## Gaps

- Whether push `extra` is written per recipient and whether pushes carry
  `userId`/`type`: needs the owned push capture below.
- Mini reactions: the Mac fetches `/rx/logmetas` for visible messages. The
  bridge still uses log details for every Mini change.
- The bucket is not persisted, so a restart refills it.
