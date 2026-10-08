# E2E emulator preparation

The local, gitignored `.lab/emu.sh` is the entry point for the two owned lab
profiles. Profile mappings, Keychain item names, and authentication helpers stay
private. Run from the repository root:

```sh
.lab/emu.sh up a
.lab/emu.sh up b
.lab/emu.sh status a
.lab/emu.sh down b
# From the maintainer's interactive terminal, when login is needed:
.lab/emu.sh up a --login
.lab/emu.sh login b
```

`up` reuses the emulator selected by its AVD name, or boots it, waits for Android
boot completion, wakes/unlocks the emulator, and launches KakaoTalk. It does not
reset userdata or create another account. `--login` submits the selected profile's
Keychain credentials once if the login form is visible. Verification, CAPTCHA,
terms, and unfamiliar screens require operator intervention; do not retry failed
login automatically. The terminal guard is an accident-prevention measure, not a
security boundary.

The public `tools/lab/emu_state.py` classifies transient UI XML into fixed state
names without printing labels, messages, identifiers, or field values.
`home-visible` is a navigation hint, not proof of a valid server session. Unknown
screens must be inspected privately before adding navigation. The existing
private `verify a` helper handles inbound SMS only; an observed refusal to send a
code must not trigger it. Outbound SMS challenges require the appropriate private
operator workflow. No backup/restore automation is in scope.

## Edge-case maintenance contract

When E2E preparation encounters a new edge case:

1. Record the client version, platform, date, sanitized observation, and method
   here; put identifying details and resumable state only in `.lab/STATE.md`.
2. Add a synthetic failing regression to `tools/lab/test_emu_state.py` for screen
   classification, or a mocked-command check for startup/navigation behavior.
3. Fix the shared helper rather than repeating ad hoc taps in each experiment.
   Require a recognizable screen and bounded waits. Unknown states stop automatic
   navigation; failed auth never becomes an unbounded retry loop.
4. Run the regression, inspect output for secrets, and document whether the fix
   was validated offline or on an owned emulator. Never commit real UI dumps.

2026-10-07 maintenance: existing helper inspection found unchecked boot timeout,
silent omission of noninteractive `--login`, and arbitrary screen-label output.
The private helper now fails on boot timeout, checks the terminal before startup
with `--login`, and emits fixed classifier states. Classification regressions use
synthetic XML. No login or verification attempt was made for this change.

Run the classifier regressions with:

```sh
python3 -m unittest discover -s tools/lab -p 'test_*.py'
bash -n .lab/emu.sh
```
