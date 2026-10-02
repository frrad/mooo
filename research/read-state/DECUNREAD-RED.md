# DECUNREAD reducer contract

The clean-room reducer accepts a copied durable room/member snapshot, one decoded
`DECUNREAD` notice, and the selected unread-query result. It returns a planned
snapshot plus ordered typed effects. It does not persist state, issue requests,
or claim that a planned member-watermark write was accepted. `CountOfNewMessage`
is the single authoritative unread field.

A missing room returns an unchanged copied snapshot and `Applied=false`. For an
existing room, current-account room side-effects run before member handling even
when the member watermark is stale or equal. When the watermark is below the
last log, the first effect is a `query_unread` descriptor with
`lowerBound=max(watermark,lastSeenLogID)` and literals `type!=10001`, `status!=5`,
`scope in {1,3}`; its selected count is then assigned. At or above the last log,
a positive unread count emits clear-unread and reset-mention-reply effects
unconditionally, followed by joined/archive refresh. Non-current notices
preserve room unread and mention state.

Member handling is gated by a non-empty active-member list, bot membership, and
the frozen type-3 room guard. A member absent from the active list produces
`active_member_add`, then a changed watermark produces `member_watermark`, then
`active_member_count` and `active_member_projection_refresh`. Changed
watermarks also schedule `member_watermark_maintenance`. A stale or equal
watermark never schedules a write or maintenance. All maps and slices are
copied, and nil map/slice shapes are preserved.

`CountEligibleUnread` exposes the reviewed reusable predicate for stored logs:
matching chat ID, positive log ID strictly greater than the lower bound, type
other than 3, status other than 5, and scope 1 or 3. This helper is independent
of the reducer's selected query-result boundary.
