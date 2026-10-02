# Token model dirty and persistence boundary

Status: bounded static contract, 2026-10-02. This document records the
reviewed model/dirty path; it does not claim durable commit, rollback, retry, or
profile-reset parity.

`NTChatContext` is a model subclass with signed 64-bit `lastTokenId` and signed
64-bit `lastLossCheckLogId`, plus signed 32-bit `lastBlindToken`. The runtime
schema declares `lastTokenId`, `lastLossCheckLogId`, and `lastBlindToken` as
non-null integer columns with default zero values. Versioned schema migrations
carry both token columns by name.

The generic model initializer receives field values, a nest, and a database
flag. It creates the field metadata and changed-fields map. For a
non-database object it first enumerates metadata defaults. Both new and
database-loaded objects then decode supplied field values through the model's
field decoder. A new object next becomes dirty and registers with the nest's
changed-object registry. Finally, both paths enumerate field metadata and
register the model as an observer for each property key. The initializer's schema loader
validates field names against Objective-C properties before building the
per-class field map.

The observer callback reads the old and new values from the change dictionary.
When they differ, it records the original old value keyed by the changed key
path, sets the model dirty, and then registers the model with the nest. The
model's `lastTokenId` setter is a direct signed 64-bit store; the dirty effect
comes from the surrounding observer path. No direct setter-to-nest call was
observed. The binary contains no class implementation of
`automaticallyNotifiesObserversForKey:`. The observer callback is therefore
observed, while the runtime policy that causes a property setter to emit that
event remains unproven; a clean-room implementation should make notification
explicit rather than relying on an undocumented runtime default.

The database coordinator later consumes the nest's changed-object registry. It
opens a transaction, classifies deleted and dirty objects, invokes each dirty
object's save operation, commits, updates in-database flags, clears the change
registry, and dispatches a change summary. Its exception path reverts changed
objects, rolls back, clears the registry, unlocks, and rethrows. Exact token
linkage into each save classification, commit-result handling, storage-error
propagation, retry behavior, and reset/restart boundaries remain unresolved.

A clean-room implementation may expose these as separate boundaries:

- field decode and property assignment;
- observable property change and changed-field recording;
- dirty-object registration;
- transaction selection and save ordering;
- commit/failure/reset policy.

The last three must not be collapsed into a claim that a token setter is already
durable.

## Synthetic contract cases

The companion vector file describes cases for an implementation-owned model
runner. Cases marked `observed` constrain only the reviewed selection and dirty
ordering. Cases marked `unresolved` must remain explicit until the missing
consumer or failure chain is independently traced.
