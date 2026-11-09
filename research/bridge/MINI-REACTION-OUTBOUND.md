# Outbound mini reactions: feasibility and remaining evidence

## Result

A two-way `😂` mapping is feasible, but sending it requires a mini-reaction
mutation implementation. Adding an alias to the six legacy selections would
send a different reaction. The incoming `웃픔` → `😂` display approximation is
already implemented; the reverse direction is not.

The exact item identifier behind the maintainer's screenshots is still
unverified. `웃픔` is display text, not a mutation identifier. The emoji is an
approximation of the supplied laughing/crying artwork, not a claim that Kakao
defines this item as Unicode `😂`.

## Evidence and confidence

Investigation: 2026-11-09 UTC (2026-11-08 local).

- Primary source: authorized Mac 26.8.0 arm64 client, using the copied private
  Ghidra project on Linux with Ghidra 12.1.3. Read-only headless queries,
  annotated instructions, and decompilation traced picker selection, mutation
  wrappers, parameter construction, shared response handling, and a failure
  banner consumer.
- Supplement: authorized Android 26.8.2 decompilation. Recovered Kotlin model
  names and serializers corroborate the endpoint, reaction kind/item fields,
  action names, and status/message response. Obfuscated implementation names
  are not treated as recovered protocol names.
- Prior observed evidence: [group reactions](GROUP-REACTIONS.md), including
  independent mini identities, simultaneous mini selections, and removal.
- Paired maintainer screenshots establish the display association only. No
  supplied screenshot establishes an item ID or the metadata field supplying
  the label. The copied text artifacts and defined Mac strings searched in
  this investigation did not resolve that ID.

Confidence is high for the existence of a separate mutation path and its
conceptual fields. Exact Mac wire encoding and end-to-end sending remain
unexecuted. No account session, emulator, or mutation was started during this
investigation. Mac runtime instrumentation was unavailable because the laptop
was disconnected. No public prior art was consulted.

## Recovered mutation model

Both clients identify the mutation path as:

```text
/emoticon/chat/rx/do
```

This is separate from `/messaging/chats/<chat>/bubble/reactions`, which the
bridge currently uses for legacy selections.

| Field or operation | Static observation |
| --- | --- |
| Target | `chatId`, `logId`, optional positive `linkId` on the Mac path |
| Revision | The selected message's mini-reaction revision is forwarded to the mutation; it is not the legacy reaction revision. |
| Item | Entries contain numeric `k` (kind) and string `o` (item identity); the traced mini picker passes kind `2`. |
| Actions | `add`, `follow`, and `remove` are separate operations under `reactions`. |
| Request tag | Mac constructs a JSON string containing `rid` from a request counter; a positive thread identifier adds `tid` inside that tag. |
| Response | Shared Mac handling reads numeric `status`; zero calls success, nonzero calls failure. It can read `message` for failure information. Android's response model requires `status` and permits an optional `message`. |

The Mac picker checks whether a matching item already exists. No matching item
selects add; an existing item owned by this account is a no-op; an existing item
not owned by this account selects follow. Removal has its own item-specific
wrapper. A bridge implementation must account for these distinctions instead
of assuming every emoji click is an add or that legacy cancel removes a mini.

### Encoding gap

Android declares a POST body with a `reactions` object whose `add`, `follow`,
and `remove` values are optional **strings containing JSON arrays**. Each array
entry contains `k` and `o`.

The Mac builder constructs a dictionary from an action name to item arrays,
passes that through its shared parameter/request layer, and separately builds
the JSON request tag. This trace does not establish how that nested parameter
is finally encoded on the wire. Some decompiler boundaries include adjacent
functions; annotated instruction checks confirm the action/path literals, but
the combined decompilation is not an executable oracle.

Do not publish an exact Mac request fixture or implement a guessed array versus
string conversion from these observations alone. The next offline native
harness should capture the completed URL request for synthetic add, follow,
remove, positive-link and thread cases, while preventing network dispatch.
That establishes HTTP method, placement, encoding, host, and header requirements
without sending synthetic identifiers to the service.

## Response, state, and failure chain

| Layer | Evidence and remaining gap |
| --- | --- |
| Picker caller | Traced item selection, existing-item/own-item checks, kind `2`, and forwarding of the current mini revision. Full availability and subscription gating remain gaps. |
| Request model | Traced target, revision, action, item and request-tag construction. Final Mac HTTP serialization remains unexecuted. |
| Response callback | Traced shared numeric status gate and success/failure callbacks. Malformed/unexpected transport-result handling needs executable cases. |
| Success consumer | The traced UI wrapper supplies an empty success callback. This does not establish every caller's behavior. |
| Failure consumer | Traced a banner path distinguishing application status `-401` (item not owned) and `-403` (count limit). These are application statuses, not HTTP status codes. Other branches exist and remain uncharacterized. |
| Persistent state | Existing research establishes mini push/revision handling. This pass does not establish a complete mutation-to-persistence chain or prove that a success response alone commits the roster. |
| Downstream bridge | Current code stores a separate mini revision and `kakao:mini:<item>` identities, but Matrix add/remove handlers currently send legacy mutations only. |

No full official-client parity is claimed. In particular, stale-revision
responses, entitlement rejection, request-tag deduplication, restart behavior,
and failure reconciliation still require controlled validation.

## What the bridge needs

1. **Verify the item.** Obtain a current official picker/search response or an
   owned mini aggregate/detail pair for `웃픔`, confirm the matching graphic,
   and record a sanitized item ID/label association. The recovered picker
   model has basic items, categories and suggested tags; the Mac exposes
   ordinary/Plus picker and search paths. A translated label alone is not a
   reliable reverse lookup key.
2. **Add the protocol/client path.** Implement bounded mini request/response
   codecs from executed native request fixtures. Preserve application failures
   and ambiguous outcomes; do not add automatic mutation retries. Establish
   whether add or follow applies and which revision the server expects.
3. **Integrate Matrix add and removal.** Resolve `😂` to the verified mini ID,
   retain `kakao:mini:<item>` for database identity, and use item-specific
   removal. Never route it through legacy cancel or legacy replacement. Keep
   other mini selections and the sender's legacy selection intact. Handle
   own-phone echoes, stale redactions, and capability advertising consistently.
4. **Validate the complete round trip.** With the shared lab lease and a
   disposable owned conversation, check Matrix add → native graphic → incoming
   echo once, follow of another member's existing item, duplicate own selection,
   removal, coexistence with legacy/other mini reactions, restart, and rejected
   or ambiguous results. Add production-path regressions and provenance-bearing
   executed/observed fixtures before claiming support.

The existing incoming display change does not rewrite reactions already sent
to Matrix. A reverse mapping should not rewrite old event keys or infer
entitlements from the presence of a translated label.

## Transfer review

Artifact ID: `mini-reaction-outbound-static-20261109`. Source class:
`binary-analysis`, with the separately supplied screenshot association.
Reviewer: Codex. Reviewed for live values, identifying paths, copied expression,
and proprietary assets; this document contains only implementation-neutral
observations and explicit gaps. Raw analysis remains in the private lab.
