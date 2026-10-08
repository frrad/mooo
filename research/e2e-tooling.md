# E2E emulator preparation

Use `research/emu.sh` for the two owned lab profiles. The local `.lab/emu.sh`
is a compatibility wrapper. Profile mappings, Keychain references, and SMS-line
identities live in gitignored `.lab/emu-config.sh`; see
[`emu-config.example.sh`](../tools/lab/emu-config.example.sh) for its schema.
The maintainer has authorized normal login and the required Privacy Policy for
the existing disposable A/B profiles; their private config retains that choice.

```sh
.lab/emu.sh ready a
.lab/emu.sh ready b
# Equivalent preparation with automatic login when needed:
.lab/emu.sh up a --login
# Launch only, inspect fixed state names, or shut down:
.lab/emu.sh up a
.lab/emu.sh status b
.lab/emu.sh down b
# Individual recovery stages, usually handled by ready:
.lab/emu.sh login a
.lab/emu.sh finish a
.lab/emu.sh verify a
```

Dependencies: macOS Keychain (`security`), Android SDK platform-tools/emulator,
Bash, Python 3.11+, and tmux. The optional XMPP SMS provider also needs `slixmpp`
in the private Python environment. No credentials are copied into config.

`ready` selects the emulator by configured AVD name, starts it in a persistent
tmux session if needed, waits for Android boot completion, wakes/unlocks it,
launches KakaoTalk directly, and navigates to Friends. Cold starts disable
snapshot loading and saving to avoid restoring stale authentication/UI state.
Login automation is guarded to KakaoTalk 26.8.2 and explicitly authorized lab
profiles. It submits the selected Keychain credentials at most once per run.

Recognized continuation steps include expired-session notices, the Google
password-manager overlay, phone permission, phone entry and required Privacy
Policy, SMS verification, skipping chat restoration, default profile photo,
contact-sync opt-out, notifications, and contact permission denial. Existing
profile names are preserved. Backup/restore operations are out of scope; the
script chooses Skip and Start, which does not recover previous chat history.
CAPTCHA, unknown terms, unfamiliar dialogs, failed authentication, and unavailable
verification stop automation. There is no automatic resend or login retry loop.

The SMS provider starts listening before the script requests a code. It supports
live messages as well as fresh archive results because a lab line can have
archiving disabled. Codes travel through a private FIFO into ADB stdin; they are
not printed in terminal output, stored in regular files, or passed in command
arguments. Provider errors and screen reports contain fixed categories. Providers
must implement `read_sms_code PROFILE READY_MARKER`, mark readiness only after
listening, and emit exactly one fresh code after the caller writes
`READY_MARKER.request`. The provider must `exec` its receiver so cancellation
stops that process; see the bundled provider for the request timestamp contract. A timed-out existing code screen can
require operator recovery; outbound SMS and voice verification are separate
unsupported routes, not inferred substitutes for inbound verification.

`home-visible` means recognized Friends navigation is visible. It is a readiness
hint, not independent proof of server authentication or message delivery. Never
use restored chat contents as proof of a valid login. UI dumps are transient,
uniquely named, and removed from the device. Emulator logs stay in private
`$HOME/Library/Caches/mooo-lab/emu/` (or `EMU_RUNTIME_DIR`), outside the
repository; never upload them without sanitization.

## Edge-case maintenance contract

Whenever preparation encounters a new edge case:

1. Record client version, platform, date, sanitized observation, and method here;
   put identifying details and resumable state only in `.lab/STATE.md`.
2. Add a failing synthetic regression to `tools/lab/test_*.py`. Classification
   tests use synthetic XML; command tests mock devices, Keychain, and SMS while
   exercising the actual helper functions. Never commit real UI dumps.
3. Fix this shared helper before resuming the experiment. Prefer screen-derived
   coordinates, bounded waits, and explicit unknown states to ad hoc taps.
4. Run regressions and secret scanning; record whether validation was offline or
   on an owned emulator. Recheck both profiles before merging startup/login work.

## Owned-account validation — 2026-10-07

Official Android KakaoTalk 26.8.2, owned A/B emulators, direct ADB observation:

- Both normal password logins and SMS verifications succeeded. Account A's
  earlier inbound-verification refusal did not recur in this session.
- Account B's XMPP archive preference was `never`; the archive-only receiver
  could not recover its code. Receiving live SMS succeeded after starting a
  listener before selecting the official client's Receive via SMS action.
- Both accounts reached Friends with their expected private profile identities.
- Cold boots through the shared launcher preserved both logins. A resumed an
  authenticated channel/chat; navigation now returns through the main tabs to
  Friends. B's contact-permission notice was dismissed without enabling sync.

Observed startup/recovery edge cases now covered by tooling: tool-shell child
lifetime, suppressed `monkey` launch failure, unchecked boot timeout, stale
snapshot risk, dummy emulator phone autofill, Back on a hardware-keyboard auth
screen, concurrent dump filenames, modal precedence over underlying screens,
and Android's alternate button ID after repeated contact denial. Synthetic
regressions cover the corresponding classification and command behavior. This
validation concerns preparation, not bridge delivery or protocol parity.

```sh
python3 -m unittest discover -s tools/lab -p 'test_*.py'
bash -n research/emu.sh
```


## Secondary-device QR approval

Prepare the phone's scanner before issuing the bridge management command
`login qr`. Select the exact fresh framework PNG from a unique folder after
verifying its bytes; do not reuse an expired image. Generation, Matrix event
anchoring, image transport, and the server deadline belong to the caller.

With explicit `ALLOW_QR_ENROLLMENT=1` in private configuration:

```sh
research/emu.sh qr-approve b
private-fresh-code-provider | research/emu.sh qr-code b
research/emu.sh qr-finish b
```

`qr-approve` waits for the asynchronous approval screen, selects persistent
`Verify with my PC`, then waits for the versioned verification form. `qr-code`
reads one four-character code from stdin, requires an empty field, verifies the
entered value and enabled confirmation control, and submits once. `qr-finish`
waits for the success dialog and chooses Close, leaving device management alone.
A private provider must select the bot code event belonging to this exact login command
and enforce its challenge/device-auth deadline. Do not enter a code in command
arguments or shell history. Neither helper generates challenges, resends codes,
or asserts final enrollment; confirm bridge `CONNECTED`, persisted login, and
restart independently.

The 2026-10-07 trial found that the approval transition is asynchronous and the
title-only verification check can run before the form is ready.
Synthetic regressions now use the actual form resource IDs and bounded waits. Account-protection and invalid-QR screens stop without approval
or retry. Unknown screens time out. The helper is guarded for Android KakaoTalk
26.8.2. Fresh B enrollment and restart passed; A reported a secondary-device
restriction ([evidence](bridge/QR-ENROLLMENT.md)).

The launcher move also exposed a provider-path bug in the example/private
configuration: SMS provider lookup now uses `TOOL_DIR`, with a failing-first
regression exercising the supplied configuration. Keep `SCRIPT_DIR` reserved
for the launcher location.

Closing successful QR approval returns to Finder rather than Friends. The
readiness helper now recognizes the versioned Finder navigation host and returns
with bounded Back navigation; synthetic classifier and command regressions
protect this recovery path.

## Prepared synthetic chat sends

After opening the exact owned direct room, enable `ALLOW_TEST_MESSAGES=1` in
private configuration. Pipe a private JSON request to:

```sh
private-fixture-provider | research/emu.sh send-text a
private-selected-photo-provider | research/emu.sh send-photo a
```

Text requests contain `peer` (exact private toolbar accessibility label),
`text` (1–512 ASCII synthetic fixture characters), and `receipt` (an absolute
new file in a mode-0700 directory outside the repository). Photo requests
replace `text` with `bounds`: the exact screen-derived Send control bounds for
one already-selected synthetic image. The phone must remain in the explicitly
selected owned chat. There is no recipient search or automatic navigation.

The helper exclusively creates a mode-0600 attempt receipt before tapping Send.
An existing receipt blocks another send, even if the previous result is
uncertain. Text entry is verified before submission. Final Matrix delivery and
source identity remain the caller's responsibility. Do not put requests in
command arguments, shell history, tracked fixtures, or logs.

Observed on owned Android 26.8.2 during 2026-10-07 A/B messaging acceptance:

- Text Send uses `send_button_layout`; photo Send uses `send_button`.
- The toolbar peer is `toolbar_default_title_text`'s content description, not
  a `title` text field or a nickname inside a message/reply.
- Clickable parents and nonclickable children can share the same label. Match
  clickable controls, not all label matches.
- Photo-gallery overlays can expose two enabled Send controls. Require the
  explicitly inspected bounds and `1 Selected, Send`; refuse ambiguity.
- With no photo permission, Allow limited access opened the system picker.
  Only the synthetic image was granted. Its MediaStore bytes were checked
  before selection. `/sdcard` and MediaStore's canonical storage path differ;
  identify the exact filename/relative path and verify bytes rather than assume
  `_data` matches the pushed alias. No broad photo-access grant is required.
- Original quality preserved the PNG exactly in the inbound Matrix transfer.
  Permission grant and gallery selection remain explicit caller operations;
  `send-photo` only submits a verified, already-selected image.
- ADB shell preflight could consume the piped JSON/code. Both chat and QR
  preflight now use `/dev/null` stdin, preserving the private input pipe.
- Shared phone XML reads now retry three transient dump/parse failures and
  remove every transient device capture. Sends themselves are never retried.

Failing-first synthetic tests cover title identity, text Send, existing drafts,
receipt reuse, duplicate gallery controls, private stdin preservation, and
transient XML failures. The shared text launcher passed on A's owned chat.
The full A/B evidence and remaining group scope are in
[direct messaging validation](bridge/DIRECT-MESSAGING-VALIDATION.md).

## Encrypted Matrix acceptance

Use the reusable Go [Matrix lab companion](matrix-lab.md) with a stable private
crypto device and exclusive send receipts. Track new harness failures there and
add regressions before considering them resolved. The owned A/B direct-room
[text, photo and restart acceptance evidence](bridge/ENCRYPTED-ROOM-VALIDATION.md)
checks the production bridge with the pinned SDK; changing the emulators' login
state is unnecessary for this test.
