# Explicit Matrix group creation

2026-10-09. Authorized KakaoTalk for Mac 26.8.0 arm64 tracing, production
regressions and controlled A/B/C encrypted acceptance. Scope: explicit regular
group creation in an existing Matrix room and once-only completion of missing
original invitations. Remaining source gaps are recorded below.

## Traced source contract

The ordinary chat-controller creation path removes self from the selected member
set and captures desired draft name/profile values. Its preparation block calls
`doCreateWithMemberIds:pushAlert:memoChat:nickName:profileImageUrl:completion:`.
The coordinator converts member IDs to its int64-array representation and invokes
`sendCreateRequestWithMemberIds:pushAlert:memoChat:nickName:profileImageUrl:completion:`.
That method constructs the request with the selected IDs and options and sends
one carriage request. This is separate from the partner-chat `PCREATE` branch.
The existing clean-room protocol `CREATE` request model uses `memberIds`,
`pushAlert`, `memoChat`, and optional `nickName`/`profileImageUrl`.

The transport callback maps a received packet to the response model; a missing
packet passes nil to completion. The coordinator distinguishes nil/non-success
from success. Success schedules work on the shared room database queue before
completion. Failure skips that success branch, records an error and dispatches
completion. The success database block passes the returned `chatRoom` to the
shared room upsert with link ID 0, full-data mode true and token 0. It supplies a
metadata-dirty handler and initializes `lastUpdatedAt` to the current time only
when that field is zero. Database completion dispatches the captured response to
the original completion. Internal upsert failure/transaction handling and downstream metadata refresh
remain gaps.

The ordinary controller's result callback stops if the controller was destroyed.
A successful result sets its chat ID, examines the returned room's certification
property, reserves the corresponding certification feed when relevant, and opens
the room. Failure constructs a source-status error using the response's error
information and calls `didFailToCreateChatRoom:`. The downstream UI has a separate
failure handler; complete status-specific behavior remains an explicit gap.
No blind creation retry is established by these callbacks.

Private reports: matrix-group-create-contract, matrix-group-create-request,
matrix-group-create-response, matrix-group-create-persistence and
matrix-group-create-consumer and matrix-group-create-store. Stack-block addresses were resolved independently
from binary disassembly. The decompiler rendered a neighboring function for one
ordinary preparation block; that rendering is not evidence of its callback body.
Proprietary disassembly and decompilation remain outside Git.

Remaining gaps: full response-property mapping, internal database upsert/failure behavior,
invitation emission and per-participant rejection behavior, complete downstream
UI failure handling, general message-free publication and status-specific
per-participant exclusion. The owned CREATE and invitation results below close
only the tested path.
Existing generic `CREATE` tests are not evidence of this bridge action or of those
untraced source behaviors.

## Intended bridge action and failure boundaries

The framework's explicit `create-group` command gathers invited/joined Kakao
ghosts selected in a Matrix room and supplies that room ID. Its provisioning
helper delegates source creation and does not itself bind that existing room.
The connector must validate participants/options and bind the returned source
chat to the intended room before reporting success.

A durable attempt record must precede the source mutation. A confirmed source
chat ID must be saved before metadata refresh or Matrix binding. Repeating a
confirmed attempt should resume binding, never call `CREATE` again. An unknown
outcome must require explicit source reconciliation. A late response, source
membership notice, startup inventory discovery, or Matrix failure must not
race into a second Matrix portal or a duplicate source group. These are bridge
safety decisions, not claimed official-client parity.

## Verification contract

Production-path regressions must exercise validation before mutation, once-only
creation, source rejection/ambiguous outcomes, durable restart state, event versus
binding races and Matrix failures. Controlled A/B/C acceptance must prove one
native group bound to the selected encrypted Matrix room, correct invitations and
roster, bidirectional encrypted messages, identity/order/deduplication, and restart
behavior. The local checks and bounded owned acceptance are recorded below;
required CI and squash merge are release gates.

## Connector behavior

The connector implements the group-creation interface and advertises an
explicit regular-group type. Validation requires an existing Matrix room, at
least two distinct non-self participants, and supported options. Room permissions, including implicit creator power in room version 12, are
validated. The login owner and bot must be joined; selected ghosts must be invited or joined, and unrelated
room members are rejected. A login-metadata journal is saved and read back before
`CREATE`. Source-status rejection is recorded without retry. Transport/decoding
ambiguity leaves an unresolved attempt. A confirmed chat ID is saved before
snapshot validation/binding. Binding checks the exact selected source roster and
uses the framework room-ID update and an explicit database save. Room membership
and permissions are checked again before binding, and source/binding work has a
30-second context bound.

Creation is serialized with incoming event admission. Startup resumes confirmed
unbound attempts before inventory discovery; an uncertain attempt fails admission
with an actionable state. Pending attempts deliberately stop incoming admission
until reconciled, preserving source continuity and preventing an inventory or
notice from creating another portal. This availability tradeoff is a bridge
safety decision.

Initial regressions passed against production input validation and SQLite-backed
journal saving/loading: uncertain attempts suppress `CREATE` after reload;
confirmed attempts can repeat failed binding handling without another mutation;
failed saves preserve prior memory/disk state; and a deleted login's zero-row
update cannot authorize creation. Existing connector tests pass with production
login metadata in their fixtures. No creation success, invitation parity or live
acceptance is claimed by these tests.

Framework regression coverage now reproduces a failed Matrix name update falsely
reporting success, then verifies pending binding and retry without another source
mutation. Binding acknowledges only after metadata flags, the joined ghost roster,
and saved portal room ID are verified. Tests also cover source rejection, lost
replies with durable reservation before transport, new unselected members during
CREATE, failed portal persistence with a changed framework cache, and concurrent
membership admission held until binding completes.

The explicit `reconcile-group <Kakao chat ID>` command selects an existing source
room for an unresolved attempt. It validates the original participant set and
current Matrix permissions before recording the source ID and binding; it sends
neither CREATE nor invitations. For a stopped login it opens the configured leased
profile for a bounded metadata session and shuts it down afterward. Framework
tests cover wrong-roster rejection, once-only reconciliation, completed-attempt
immutability and temporary-owner release. Shutdown failure retains the owner for
explicit disconnect cleanup and does not authorize another profile open.

## Initial partial result and recovery

One encrypted Matrix create command using the existing B secondary profile
confirmed a regular source group. Fresh CHATINFO/MEMLIST/MEMBER showed B and A,
but C was absent, although A/C were selected. The shared title matched the
requested name. No ordinary source message was sent, and the bridge left the
confirmed source ID pending rather than reporting successful binding or repeating
CREATE. The B native chat list did not expose the named room at this point.
This does not establish whether a first message publishes the room or why C was
excluded. The cause remains unproven. The explicit invitation recovery and later
acceptance are recorded below.

Mac class metadata independently confirms a LocoCreateResponse with int64 chatId
and a LocoChatRoom object, and request int64 memberIds, boolean pushAlert/memoChat,
optional nickName/profileImageUrl. No per-participant failure field is declared
on that response class. This does not rule out inherited or nested error data.

The observed partial roster is projected into the synthetic
`group-create-partial-observed.json` fixture. A production framework regression
feeds that roster to creation, verifies the confirmed source ID remains pending,
and repeats the bridge action to verify no second CREATE or Matrix binding.

For the owned experiment, adding the selected C identity as B's friend succeeded
and returned the expected identity. A subsequent fresh source roster still had
only A and B. This does not establish the cause of C's exclusion or prove that
friendship is sufficient for invitation. No invitation or second CREATE has been
sent in this investigation.

## Invitation chain under investigation

The Mac ordinary invitation controller calls
`doAddMemWithChatRoom:memberIds:completion:`; TeamChat takes a separate path.
The coordinator sends one request with the existing chat ID and selected int64
member IDs. A received packet becomes a typed response; missing packets pass nil
to completion. The response declares a chat log and warning message.

The coordinator's failure branch dispatches the response without its successful
database work. With a successful response containing a chat log, its database
block processes that log with status 3 and refreshes the selected members using
the existing chat and link IDs. Database completion dispatches the response.
Success without a chat log dispatches completion directly. These observations
come from independently resolved callback addresses in Mac 26.8.0; internal log
storage and member refresh failure handling remain explicit gaps. They do not
establish an automatic ADDMEM following CREATE.

The ordinary invitation callback guards against a destroyed controller, then
dispatches its result handler. That handler reads response error information and
adds a nonempty warning under `warningMsg`. It constructs the error using the
response status and calls `didInviteMembers:withError:`, which forwards to the
chatting-controller delegate. For a supplied completion, the handler looks up the
stored message by returned chat/log IDs and passes its `inviteeUserIds` only when
the message has the expected invitation-message class; otherwise it passes nil.
This is evidence of an explicit invitation-result path rather than proof that
all requested IDs were accepted. The main window delegate presents special alerts
for error codes 53/54, a separate HelloPass path, supplied error-information
alerts, and a nonempty `warningMsg` alert. No retry is called in this delegate
body; alert completion callbacks and the two other delegate implementations
remain untraced. Exact generic response deserialization remains a gap. Private reports include
matrix-group-create-addmem-store, matrix-group-create-addmem-ui,
matrix-group-create-addmem-ui-result, matrix-group-create-addmem-ui-error and
matrix-group-create-addmem-ui-delegate.

The base LocoModel's name-mapping and required-validation-field methods return
nil; ADDMEM request/response class metadata declares no override. LocoRequest
obtains its superclass object dictionary before optional validation/name mapping,
and creates a packet from its command and that dictionary. LocoResponse initializes
from the packet body through `initWithJSONObject:`. The inherited model copies a
dictionary before applying any name mapping and delegates property decoding to
its superclass. This narrows the mapping gap; detailed superclass coercion and
malformed-input behavior remain untraced. Reports: matrix-group-create-base-models,
matrix-group-create-base-codec, matrix-group-create-loco-model and
matrix-group-create-model-mapping.

## Explicit completion of a partial creation

`complete-group-invitations` acts only on this Matrix room's confirmed, pending
creation. It checks room authority and the fresh source roster, rejects unrelated
source members or an absent creating account, and selects only missing members
from the original durable selection. It saves and reads back an invitation
reservation before one ADDMEM. A failed reply or warning leaves the reservation
pending; another invocation cannot send that invitation again. With no warning,
binding still requires a fresh complete roster. If the roster converges later,
reconciliation can bind the same source group without another mutation.

Production framework tests cover successful missing-member invitation and binding,
lost replies, warnings, partial acceptance, suppression after journal reload,
and a journal-save failure before transport. Additional cases reject an unknown
source identity, invalid saved selections, unrelated Matrix/source members,
non-regular groups and an absent creating account. The absent-account regression
exposed that the general chat-info path inserts self into the framework member
map. Creation now independently validates positive, unique, selected raw MEMLIST
identities and creator presence before invitation and binding. These are synthetic
safety tests, not official-client execution parity.

The owned lab sent one encrypted completion command on 2026-10-09. Its durable
journal reserved one missing invitee and recorded successful binding to the
selected Matrix room with the exact chosen name. After stopping the bridge, a
fresh probe of the original secondary profile confirmed the same regular group,
three unique members, active count three, and both selected identities present.
No second CREATE or ordinary message was sent. Each owned A/B/C native Chats list
then showed exactly one room with the selected name. Thus this experiment did not
need an artificial first text to make the group visible after ADDMEM. It does not
establish general message-free CREATE visibility. Encrypted message exchange,
ordering/deduplication and restart acceptance are recorded below.

## Owned encrypted acceptance

On 2026-10-09, the bound bridge restarted using the original B secondary profile.
A and C each sent one unique synthetic text in the created native group. The
selected Matrix room received two encrypted events with distinct source IDs and
the existing selected ghosts. The retained SDK crypto store decrypted each event
and matched its exact text. One SDK-encrypted Matrix text was then forwarded to
the same native group; A/B/C each displayed all three test texts exactly once.

With the bridge stopped, C sent another unique text. A normal restart recovered
one encrypted event with C's unchanged identity and exact decrypted content.
The three earlier source/Matrix mappings remained unchanged. A second normal
restart retained all four mappings, one source portal and one binding to the
selected Matrix room. Earlier A/C events still decrypted using the retained SDK
keys. The Matrix room continued to use Megolm encryption. These observations
cover this regular-group creation and explicit invitation-completion path; they
do not claim general membership lifecycle, TeamChat or account-restriction parity.

## Operator actions

In the intended Matrix room, invite the selected Kakao ghosts and the bridge bot,
ensure the owner and bot have the required state permissions, then run
`!kakao create-group regular`. The selected room name becomes the requested group
name. At least two other Kakao identities are required. Unsupported options and
unselected room members are rejected before CREATE.

A confirmed partial result preserves its source group ID. After inspecting the
source outcome, `!kakao complete-group-invitations` explicitly sends one invitation
for missing original selections. It never creates another group. A warning,
rejection or lost reply leaves that invitation reserved; inspect and use
`!kakao reconcile-group <chat ID>` once the original roster is complete. An unknown
CREATE outcome likewise requires selecting an existing matching source group
through reconciliation. Reconnect the login after stopped-profile reconciliation.

Do not delete the journal to retry an uncertain mutation. The durable record and
source membership are the authority for recovery. These commands do not manage
general membership changes or bypass source invitation restrictions.
