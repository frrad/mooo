# SharpSearch (type 23): availability and tracing

## Evidence

On 2026-10-09, the maintainer-owned Android 26.8.2 sender exposed Walkie Talkie
in the optional chat-input control and no search-labelled entry in the
attachment picker. Entering `#weather` in the owned peer chat left it as ordinary
composer text, with no search control IDs exposed. The draft was cleared without
sending. These observations do not establish that all entry points are absent.
No controlled type-23 payload has been captured, and mooo retains its unsupported
message notice.

## Independently derived static leads

Scoped inspection of the owned APK found the following chain:

- `SharpSearchInputHelper` (`nub1`, classes8) obtains its search-tag setting from
  `TalkPreferencesWithBlocking.R2` (`q1g1`, classes12). That setting returns false
  unless the core SharpSearch availability predicate is true.
- The core flag accessor (`hkt`, classes10) selects `FlagImpl` (`fkt`), whose
  availability implementation is `zh2`. The `yh2` interface names predicate `f`
  `isSharpSearchEnabledWithBlocking`; its coroutine calls `h`, which checks
  `USE_SHARP_SEARCH` against a stored availability value. The enum assigns this
  flag masking position 19; the local check requires its mask bits to be present.
  `Flag` storage (`ckt`, classes10) reads the long-valued `available2` preference
  with default zero. `TalkPreferences` (`p1g1`, classes10) uses the
  `Feature_DataStore.pref` store. The origin/update lifecycle and the actual
  owned-account flag value remain untraced. A scoped read on the owned emulator
  could not access that preference; no recovered account value is claimed.
  A regional or entitlement explanation is not established.
- `ShareManager.D` (classes7) handles forwarding an existing type-23 log through
  the generic structured-message intent, with its message and attachment. This
  is a forwarding consumer, not proof of an available original emitter.
- `WebInterfaceSchemeProcessor` (classes7) handles
  `app://kakaotalk/webinterface/requestresults` by locating an existing
  SharpSearch log in the selected room, producing its results with current chat
  context, and passing them to the requested WebView success callback.
- `SharpSearchLog` (`oub1`, classes9) records interactions using attachment
  metadata. Request constructors, response handling, persistent state changes,
  renderers and failures still need complete tracing.

These are static leads rather than executed parity fixtures. ShareManager's
scoped decompilation reported errors and supplied partial output; only the
specific readable consumer above is recorded. No proprietary source or assets
are included here.

## Next steps and scope

Trace the ordinary input/view-controller entry, availability storage updates,
original send path, response callbacks, persisted log and downstream renderer.
If an owned account exposes an eligible normal flow, capture a controlled share
and verify it through the encrypted Matrix bridge and native receiver. Do not
turn a named constant, a forwarding intent, or a crafted unsupported payload into
an E2E support claim. Do not modify feature flags to bypass eligibility.
