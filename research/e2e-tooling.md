# E2E emulator preparation

Use `tools/lab/emu.sh` for the two owned lab profiles. The local `.lab/emu.sh`
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
bash -n tools/lab/emu.sh
```
