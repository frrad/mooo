# DECUNREAD reducer RED handoff

This is an implementation-neutral contract proposal for independent review.
The executable characterization is intentionally RED: the production reducer
and effect types are absent until the member-watermark and bulk-update review
is complete.

The proposed pure API accepts a durable room/member snapshot, one decoded
`DECUNREAD` notice, and query inputs. It returns a copied snapshot plus an
ordered list of typed effects. Input maps are borrowed read-only and must not
be mutated. A member-watermark effect is a planned write; the reducer does not
claim that storage accepted it.

The current vectors cover the observed room gate, current-account branches,
lower-bound descriptor, stale/equal current-account routing, immutable input
ownership, strict JSON fixture decoding, and effect ordering. A stale or equal
member watermark must not suppress the outer current-account unread/joined/
archive branch; member-write suppression is a separate helper decision. The
vectors deliberately omit active-member additions and frozen-room handling
until the helper's state inputs and bulk-update semantics are reconciled.

The first effect for the below-last-log branch is an explicit `query_unread`
descriptor carrying `chatId` and the clamped lower bound. The query's excluded
type/status and allowed-scope literals remain separate reviewed inputs rather
than being guessed by the reducer.

Open review questions are whether stale/equal member watermarks are filtered
before the planned effect, whether the bulk helper can accept multiple pairs,
and which room field identifies the frozen special type.
