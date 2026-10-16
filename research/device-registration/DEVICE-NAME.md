# Device name, model and OS version in the Mac registration request

Status: static trace complete, 2026-10-16, with one earlier owned-account
observation that matches it. Evidence IDs DR-MAC-025 to DR-MAC-030 and DR-OBS-031
in `EVIDENCE.md`; method in
`experiments/DR-EXP-004-device-name-source.md`.

This note answers one question: how the official macOS client arrives at the
`device.name` it sends when it registers as a secondary device, and how that
compares with mooo. The `model` and `osVersion` siblings come from the same
helper, so they are covered too.

## Summary

- `name` is the macOS **Computer Name** (the user-facing name from System
  Settings, for example `Alex’s MacBook Pro`). It is not the DNS host name or
  the Bonjour local host name. Nothing removes a domain suffix, because the
  Computer Name has none.
- If the Computer Name cannot be read, the client falls back to the hardware
  model identifier (`hw.model`, for example `Mac14,2`). If that also fails, it
  sends an empty string. It never sends a fixed product string.
- The client does not truncate, filter, escape (beyond standard JSON string
  encoding), localize, prefix or suffix the value.
- The value is read again for every generate request. The client does not
  store it, so a renamed Mac sends its new name the next time it registers.
  Nothing re-sends the name for a device that is already registered.
- `name` is sent only in the full `device` object of passcode generate and QR
  generate. It is never sent in poll, register, cancel or login requests, and the
  LOCO `LOGINLIST` has no name field.
- `model` is the hardware model identifier (`hw.model`), not the word `Mac`.
  `osVersion` is `major.minor.patch`, always three numbers, from the running OS
  version.

## Value chain

### Sources, in order

All three values come from one Mac-client helper that builds the full `device`
object as a string-to-string dictionary. The client calls this helper fresh
each time it builds a passcode-generate or QR-generate request.

| Field | Source | Fallback |
| --- | --- | --- |
| `name` | Computer Name helper (below) | helper result `nil` → empty string `""` |
| `uuid` | Hashed device UUID helper (see `PROTOCOL.md`) | `nil` → `""` |
| `osVersion` | `NSProcessInfo.operatingSystemVersion` formatted as `"%ld.%ld.%ld"` | none (always present) |
| `model` | `sysctlbyname("hw.model")` decoded as UTF-8 | `nil` → `""` |

The Computer Name helper:

1. Calls `SCDynamicStoreCopyComputerName(NULL, NULL)`. A non-`NULL` result is
   returned unchanged. The client does not check whether it is empty.
2. If that returns `NULL`, it returns the `model` helper result: it reads the
   `hw.model` size, returns `nil` when that size is zero, and otherwise reads the
   value and decodes it with `-[NSString stringWithUTF8String:]`.

So the order is **Computer Name → hardware model identifier → `""`**. The
client never consults the host name (`gethostname`, `NSHost`,
`NSProcessInfo.hostName`, `kSCPropNetLocalHostName`) or any user-set value of
its own for this field. The binary does reference host-name APIs, but only in
unrelated features.

### Transforms

- Foundation `NSString` is bridged to a Swift `String`, which preserves the value
  exactly.
- The `[String: String]` dictionary is converted to `[String: Any]` without
  changing any value, then set as the request's `device` parameter.
- The request is sent as JSON (see `PROTOCOL.md`, shared HTTP session profile),
  so the name is ordinary JSON string text: UTF-8, with quote, backslash and
  control characters escaped. Spaces, typographic apostrophes and non-ASCII
  letters are sent as they are.
- There is no length limit, trimming, character filter, normalization,
  localization, prefix or suffix. Any limit is the server's (unknown, see
  gaps).

### Consumers

| Request | Carries the name? | Key | Source |
| --- | --- | --- | --- |
| `/mac/account/passcodeLogin/generate` | yes | `device.name` | full `device` helper |
| `/mac/account/qrCodeLogin/generate` | yes | `device.name` | full `device` helper |
| passcode register / cancel, QR cancel / login | no | UUID-only `device` | — |
| QR password check | no | — | — |
| LOCO `LOGINLIST` | no | — | — |
| `/mac/account/login.json` (email/password login, not the QR or passcode secondary-device flow) | yes | flat `device_name` | the same Computer Name helper, read directly |
| `/mac/account/request_sign.json`, `/mac/account/confirm_sign.json` | yes | flat `device_name` | the same Computer Name helper, read directly |

The last two rows are other account flows that mooo does not use. They show
that the Computer Name helper is the client's only source of a device name.
mooo should not add these flows because of this note.

### Persistence and rename

The client calls the Computer Name helper each time it builds a request. No
preference, keychain item or database column holds the name, and no code reads a
stored name back. Renaming the Mac changes what the next generate request sends.
The client has no request that updates the name of an already-registered device,
so whether the device list changes after a rename depends on the server
re-registering the device (not traced, see gaps).

### Failure

| Computer Name | `hw.model` | Sent `name` |
| --- | --- | --- |
| present (any string, including `""`) | not read | that string, unchanged |
| unavailable | present | the model identifier |
| unavailable | size 0 / unavailable | `""` |

The request is still sent when `name` is empty. Nothing on the client stops a
generate request because the name is empty. In the email-login row above, the
name goes into an Objective-C dictionary literal without a `nil` check, so a
`nil` name there would raise an exception instead of sending an empty string.
That flow is out of scope for mooo.

### Observed corroboration

During the 2026-09-20 owned-account QR approval (DR-DSP-001), the Android
primary's approval screen showed the Mac's default Computer Name: a possessive
first name, a typographic apostrophe, spaces and a product name. A host name
cannot contain those characters, so the value the server stored and displayed
was the Computer Name, not a host name. The lab value itself is not recorded
here.

## mooo today

mooo's QR login creates each new profile with `authstate.Config` and sends the
stored identity in `registration.FullDevice`
(`internal/bridge/connector/login.go`, `internal/protocol/registration/form.go`).
The manual `auth init` command takes the same four values as required flags,
and its option parser rejects empty values for every flag.

The middle column is mooo before PR frrad/mooo#413. The last column is mooo
after it.

| Aspect | Official Mac 26.8.0 | mooo before #413 | mooo after #413 |
| --- | --- | --- | --- |
| `name` source | Computer Name | constant `mooo-bridge` | `network.device_name` when set; otherwise the Computer Name via `scutil --get ComputerName` |
| `name` fallback | `hw.model`, then `""` | — | `hw.model`, then `""`; always `""` off macOS when unset |
| `name` transforms | none | none | none |
| `name` validation | none (empty allowed) | rejects empty or whitespace-only, invalid UTF-8, or more than 256 bytes | valid UTF-8; empty allowed; at most the 4 KiB QR-request field limit |
| `name` lifetime | read again for every generate | stored in the profile when it is created and reused | same as before #413 |
| `model` | `hw.model` (e.g. `Mac14,2`) | constant `Mac` | constant `Mac17,3`, pinned since frrad/mooo#416 |
| `osVersion` | running OS, `major.minor.patch` | constant `26.6.2` | constant `26.6.2`, pinned since frrad/mooo#416 |
| Requests that carry it | passcode/QR generate only | QR generate only | QR generate only |
| LOCO name field | none | none | none |

Where mooo matches:

- The name is sent only in the full `device` object of QR generate, and never
  in poll, cancel or `LOGINLIST`.
- The name is not transformed on the wire: mooo sends the stored string as a
  JSON string.
- The `osVersion` format is three numbers separated by dots.

Where mooo differs:

1. **Source.** Before #413 mooo sent `mooo-bridge`, a product string the
   official client never sends. An earlier draft of #413 used the DNS/kernel
   host name, a different macOS setting from the Computer Name: on a Mac it
   gives `Alexs-MacBook-Pro` where the official client sends
   `Alex’s MacBook Pro`. #413 follows the official chain when
   `device_name` is unset, and the operator's value when it is set. The
   configured value is the portable equivalent of the user-set Computer Name.
2. **Fallback.** Before #413 mooo could not send `""`, because `authstate`
   rejected empty names. After #413 it falls back exactly as the official
   client does, including `""`. Off macOS, and so in Docker, an unset
   `device_name` always sends `""`; the example config tells operators to set
   it there.
3. **Computer Name source API.** mooo reads `scutil --get ComputerName` (the
   stored SystemConfiguration preference) because it avoids cgo. The official
   client calls `SCDynamicStoreCopyComputerName` (the dynamic store). They are
   expected to agree; see gaps.
4. **Lifetime.** The official client reads the name again for every generate.
   mooo stores it when the profile is created. This matters only if mooo ever
   regenerates for an existing profile. Today each QR login creates a new
   profile, so the difference cannot be seen.
5. **`model` and `osVersion` (deliberate).** The official client reads the
   host's `hw.model` and running macOS version. mooo sends constants instead:
   `Mac17,3` and `26.6.2`, both read on 2026-10-16 from one real Apple-silicon
   Mac that runs the pinned 26.8.0 app. Most bridges run on Linux or in Docker,
   which have neither value, so reading the host would not help them, and a
   pair taken from one real machine cannot combine a model with a macOS
   version it never shipped with. A bridge on a real Mac therefore does not
   report its own hardware or OS. Refresh both values together with the pinned
   app version. Before this change mooo sent `model: "Mac"`, a value the
   official client never produces.

## Recommendations

These were written before PR frrad/mooo#413, which implements 1, 2, 4 and the
fixture half of 6. The maintainer chose official-client behaviour over
recommendation 3: an empty name is sent rather than a placeholder.
PR frrad/mooo#416 implements recommendation 5.

1. Let the operator set the name in bridge configuration (for example
   `device_name`). Use it when it is non-empty, and send it unchanged apart from
   UTF-8 validation and the 4 KiB QR-request field limit. This is the portable equivalent of the user-set Computer
   Name.
2. When no name is configured and the host is macOS, use the Computer Name
   (`scutil --get ComputerName`, or `SCDynamicStoreCopyComputerName` if cgo is
   acceptable) rather than `os.Hostname()`. Do not remove dots or rewrite the
   value. Then fall back to `hw.model`.
3. *Superseded by #413, which sends `""` as the official client does.* On
   non-macOS hosts, or when every source is empty, use a non-empty
   deliberate default (keeping `mooo-bridge` is fine) and record it as a
   deviation from the official `""`. A host name is acceptable as an
   intermediate fallback, but it should not be described as official parity.
   Removing a domain suffix is a mooo choice, not something the official client
   does.
4. Prefer a configuration value over Docker `hostname:`. Setting the container
   host name to name the Kakao device couples two unrelated settings.
5. Done: `model` and `osVersion` are pinned from one real Mac (difference 5
   above). A QR login regression test checks the generate request.
6. When implementing, have the Go tests read
   `research/fixtures/device-registration/device-name.json`. *The suggestion
   to record the `""` fallback as a `mooo_deviation` is superseded: #413
   matches it.*

## mooo live acceptance (2026-10-16)

Method: three fresh QR enrollments of the mooo bridge (PR frrad/mooo#413) on an
owned disposable account, approved from the owned Android primary (KakaoTalk
26.8.2), each with its own empty profile directory and bridge database. This
observes mooo and the server, not the official Mac client, so it is not a
parity claim for the fallback chain.

| Path | Sent `device.name` | Primary approval and success screens | Primary's Registered Desktops entry | Bridge |
| --- | --- | --- | --- | --- |
| `device_name` set to a synthetic Mac-style name | that name | that name, unchanged | that name | `CONNECTED`; reconnected after restart without a new QR |
| unset, native macOS 26.6.2 | the Computer Name read with `scutil` (typographic apostrophe, spaces) | that exact string | that exact string | `CONNECTED` |
| unset, Linux container | `""` | `PC` | `PC` | `CONNECTED`; reconnected after restart without a new QR |

Observations:

- The server accepts an empty `device.name` in QR generate and completes
  enrollment. The primary labels such a device `PC`.
- The server and primary showed the configured name and the Computer Name
  exactly as sent, including a typographic apostrophe and spaces.
- With an empty name, the primary's approval screen used different
  explanatory text from the named cases. The meaning of that difference is not
  known.
- The account's Registered Desktops list allows five entries. Each enrollment
  added a row; none replaced an existing one.

The lab names and account values are not recorded here.

## Gaps

- **Not executed or observed in isolation.** The fallback order, the empty-string
  result and the lack of transforms come from static reading. The only owned
  observation (DR-OBS-031) covers the ordinary case in which the Computer Name
  is present. A lab harness that hooks `SCDynamicStoreCopyComputerName` and
  `sysctlbyname` in the instrumented debug copy and calls the full `device`
  helper would turn the fixture into `executed`. It would require quitting the
  official client first (see DR-MAC-022), which this session did not do.
- **Server handling.** The live acceptance above shows the server accepts an
  empty name (shown as `PC`) and keeps spaces and a typographic apostrophe.
  Whether it limits, trims or rejects long or otherwise unusual names, and
  whether re-registering after a rename updates the existing entry or creates
  a new one (for example through `previousId`), is still unknown.
- **`scutil` versus the dynamic store.** mooo reads the Computer Name with
  `scutil --get ComputerName`, which reads the stored preference. The official
  client reads `SCDynamicStoreCopyComputerName`. Whether the two can ever
  differ (for example right after a rename, before configd updates the
  dynamic store) has not been tested.
- **Sandbox behaviour.** It is not proven that the App Store sandbox never makes
  `SCDynamicStoreCopyComputerName` return `NULL`. The observation shows that it
  succeeded at least once on the reviewed build.
- **Other platforms.** The Android and Windows clients were not consulted. They
  are not needed for Mac parity, and they use different platform name sources.
- **Prior art.** No public prior art was consulted for this note.
