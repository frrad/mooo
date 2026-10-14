# History HTTP dependencies and validated resource reuse

Implementation checkpoint: 2026-10-14. Synthetic production-path verification;
no live Kakao experiment or real Beeper account was used for this slice. This
is an implementation policy, not evidence of the official client's rate limits.

## Contract

ReactionMembers, ReactionMetaSync and MiniReactionDetails now enter the same
account admission as history and read-only LOCO metadata. The merged reaction
reader uses those public methods. Their complete typed response is decoded before
successful admission retires the durable error reservation. Concurrent identical
reads coalesce within a device profile; current reaction results are not stored
as immutable resources. Reaction writes retain their existing mutation path.

Bridge message conversion and history page conversion propagate the owning
client into downloads. Photo, audio, video, file, contact, multi-photo,
sticker and mini-text resources use the admitted reader. Profile avatars capture
the owner too. The HTTP transport preserves existing downloader URL, redirect,
length and content checks. Admission holds the shared account lease until the
whole downloader has validated its result, including checksum and decryption
where the existing media API requires them. Failed validation retains backoff.

Physical HTTP exchanges, including redirects, are counted individually. The
first dispatch uses normal admission; subsequent exchanges wait the configured
minimum interval after the previous body closes, reserve error backoff before
sending, and stop at 16 exchanges. The existing per-demand duration and queue
bounds still apply. Production fetchers close response bodies; this internal
callback API requires callers to join transfers and close bodies before returning.
A custom opaque HTTP Doer can only be counted at its exposed Do boundary; the
production http.Client path instruments its underlying redirect transport.

## Private resource cache

Fully validated immutable resource results are stored beside the selected
profile in `.history-resources`, bound to its account/device identity. Keys
include the complete attachment or versioned resource identity and are hashed
for filenames. Sticker bundles serialize the already validated typed result.
Neither bodies nor resource URLs appear in the account scheduling journal.

The directory is private (0700), files are private (0600), and writes use a
synced temporary file followed by rename and directory sync. Each file includes
binding, length and SHA-256 integrity data. Incorrect binding, permissions,
length or digest fails closed instead of silently downloading again. The cache
allows at most 64 MiB per result and 256 MiB of resource files, evicting least
recently accessed entries when needed. Cache hits bypass source admission and
return independent bytes; a versioned key change causes a new admitted read.
Eviction can cause a later re-fetch and is not a promise of permanent retention.

Mutable reactions and avatars are only coalesced in flight. An avatar URL alone
does not establish immutable content, so it is not persisted in this cache.
Existing Matrix avatar identity handling is unchanged. Bootstrap authentication
and inventory still have separate paths; this work does not claim that every
network request has joined history admission.

## Checks and remaining acceptance

Race-enabled tests exercise real SDK opening/admission, concurrent reaction
reads, malformed HTTP 200 backoff, shared HTTP/LOCO exclusion, redirect spacing,
the 16-exchange ceiling, cache restart reuse, changed keys and corruption refusal.
A bridge photo test uses the real SDK cache and actual validated conversion:
exact synthetic PNG bytes reach the upload sink both before and after SDK
restart, with only one source HTTP request. This sink does not prove encrypted
Matrix insertion or native client display.

The suite also reproduced a bootstrap/disconnect cleanup race: forced bootstrap
cleanup could duplicate another owner's Shutdown. A regression fails before the
fix; cleanup now claims the retained owner atomically and releases that claim on
both success and failure.

Required completion gates remain owned HTTP content/read acceptance, encrypted
history delivery and restart recovery, a deliberate viewing-hook dispatcher, and
the complete official clean-login/open/scroll evidence. The three-flow goal is
still incomplete; no live account stress experiment is justified by these tests.
