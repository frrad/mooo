# Operator chat listing

Research date: 2026-10-09. Controlled owned disposable-account observation with
Android KakaoTalk 26.8.2 as primary and mooo's existing secondary profile.
No official profile/session import, account creation or new QR enrollment.

## Command and scope

Build `cmd/mooo-lab` with `GOFLAGS='-tags=goolm'`. Run:

```sh
mooo-lab chats list --state /absolute/private/profile --output /absolute/private/chats.json
```

Select one existing operator-owned profile explicitly. Stop its bridge first;
the command uses the existing exclusive profile lease. The output parent must
be private (0700); the JSON file is created exclusively at 0600 and never
replaces an existing file. Keep the output outside Git. Only the count goes to
stdout. The JSON contains room IDs, types, available names, member counts and
unread counts; it excludes message bodies, credentials and profile resource URLs.
Normal returned errors remove the reserved output. Abrupt process termination
can leave an incomplete file; inspect and remove it before choosing a fresh path.

`Client.OpenWithOptions` with `FullChatList` selects a zero local list token for
the next LOGINLIST while retaining committed chat/max-log pairs and LBK. It does
not reset the stored checkpoint or bypass the lease. `Client.ListChats` returns
sorted typed metadata from the completed full login snapshot. It does not fetch
message history or acknowledge reading. The ordinary resumed client continues
using its durable token; its delta snapshot cannot masquerade as a full listing.

Each accepted page applies deletions before metadata updates. A later deletion
removes an earlier recovery target and its metadata; same-page recreation retains
new metadata but does not inherit the deleted message boundary. Repeated entries
use the latest page metadata. Only status-zero EOF establishes completeness.
Partial statuses and pagination failure do not produce successful output or
replace the known inventory. Existing pagination is bounded at twenty pages.
Malformed typed metadata fails the listing rather than silently omitting rooms.
Names may be absent; this command does not synthesize names from contact lookups.
The snapshot is fixed at login, not a live subscription; run a new command for
another snapshot.

## Evidence and limits

The official deletion/update and successful-EOF contracts were traced previously
in [message-continuity.md](message-continuity.md) and the session-login evidence.
Synthetic encrypted backend tests drive production LOGINLIST and LCHATLIST paths;
new regressions cover partial full-list retention and removal of an earlier-page
recovery target. Typed listing tests cover empty rooms, malformed metadata,
latest updates and recreation. A wire test confirms full mode sends token zero
while preserving durable chat/max-log pairs and LBK and leaving its input
checkpoint unchanged. These synthetic tests are not official-client parity proof.

The live owned-secondary command completed and included the existing owned peer
room. Before/after checks showed unchanged committed message positions, private
output permissions and release of the exclusive lease. Private receipts and
identifying summaries remain outside the repository. This observation establishes
the tested account path, not all room kinds or server pagination branches.

The initial operator run exposed a LOGINLIST binary-field shadow discrepancy.
The new operator command defaults to diagnostic logging, like the bridge;
research commands retain panic mode and explicit `MOOO_BSON_SHADOW` selection is
respected. The diagnostic remains visible. Alternate official BSON reader routing
is still untraced; no general claim that the official client ignores the field is
made. See [BSON shadow scope](reconnect/BOUNDED-BSON-VALIDATION-API.md).

Contact profile-photo retrieval and owned multi-participant acceptance are separate
pending work. This listing does not establish either capability.
