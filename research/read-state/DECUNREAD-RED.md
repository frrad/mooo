# DECUNREAD reducer contract

The clean-room reducer accepts a copied durable room/member snapshot, one decoded
`DECUNREAD` notice, and the selected unread-query result. It returns a planned
snapshot plus ordered typed effects. It does not persist state, issue requests,
or claim that a planned member-watermark write was accepted.

A missing room is not applied. For an existing room, current-account room
side-effects run before member handling even when the member watermark is stale
or equal. When the watermark is below the last log, the first effect is a
`query_unread` descriptor with `lowerBound=max(watermark,lastSeenLogID)` and
literals `type!=3`, `status!=5`, `scope in {1,3}`; its selected count is then
assigned. At or above the last log, unread state is cleared. Joined/archive
refresh effects follow the unread branch. Non-current notices preserve room
unread and mention state.

Member handling is gated by a non-empty active-member list, bot membership, and
the frozen type-3 room guard. A member absent from the active list is added and
its active-member count is refreshed. That addition does not imply a watermark
write when the stored watermark is newer or equal. A watermark is planned only
when absent or strictly newer. All maps and slices are copied, and effects are
ordered as returned.

`CountEligibleUnread` exposes the reviewed reusable predicate for stored logs:
matching chat ID, positive log ID strictly greater than the lower bound, type
other than 3, status other than 5, and scope 1 or 3. This helper is independent
of the reducer's selected query-result boundary.
