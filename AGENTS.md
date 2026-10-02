# Repository instructions

## Mission

Build a public, self-hostable KakaoTalk secondary-device implementation in Go,
followed by a Matrix/Beeper bridge. Prioritize protocol understanding and safe,
reproducible research before bridge features.

## Product scope

- Kakao cloud backup and cloud restore are out of scope. Restore-only official-
  client behavior may be documented when it prevents a false parity inference,
  but do not implement or live-test backup/restore support unless the maintainer
  explicitly changes this scope.

## Working style

- At the start of a local research session, read `.lab/STATE.md` if it exists.
  It is the gitignored handoff log for account-independent operational details,
  private lab paths, and resumable state. Keep public facts in `research/`; never
  copy secrets or identifying lab values from `.lab/` into tracked files.
- Prefer `gpt-5.6-luna` subagents for bounded execution tasks where speed and cost
  matter, including searches, inventories, routine implementation, mechanical
  refactors, and test runs.
- Use stronger reasoning for architecture, protocol/security analysis, synthesis,
  and decisions whose mistakes could expose credentials or accounts.
- Keep work autonomous and goal-oriented. Record durable plans in `PLAN.md` rather
  than opening GitHub issues unless the maintainer asks for issues.
- Do all work on feature branches and land it through pull requests; never
  commit or push directly to `main`.
- Make small, reviewable commits. Pull requests use squash merges and require
  passing CI before merge.
- The maintainer authorizes agents to merge their own pull requests once all
  required CI checks pass and GitHub reports the PR as mergeable. Always use a
  squash merge; never merge with pending or failing required checks.
- Once a self-contained, reviewable piece of work is done and validated locally,
  open a pull request for it and merge it as soon as the required CI checks
  pass. Do not wait for the maintainer to ask for the PR or the merge.

## Research safety

- Only inspect accounts, clients, devices, and traffic that the maintainer owns or
  is explicitly authorized to test.
- Never commit credentials, session material, tokens, device identifiers, phone
  numbers, private messages, raw packet captures, decrypted databases, proprietary
  binaries, or other account-specific artifacts.
- Store sensitive lab artifacts outside the repository. Check staged changes and
  generated logs for secrets before every commit.
- Prefer disposable test accounts. Do not run experiments against a primary
  account unless the maintainer explicitly requests it.
- Keep an evidence trail: client version, platform, experiment date, method,
  sanitized observation, confidence, and source/provenance.
- Reverse the authorized official clients and perform controlled owned-account
  observations before consulting public prior art. Use prior art only afterward
  to confirm, challenge, or contextualize independently derived findings; never
  use it as the starting point for a protocol implementation.
- For protocol parity work, trace the complete official-client chain: request
  model, response model, callers/callbacks, persistent state changes, downstream
  consumers, and failure behavior. Do not infer parity from a request constructor
  or one successful live path; record any untraced layer as an explicit gap.
- Separate observed facts from hypotheses and implementation decisions.
- Do not copy decompiled source or proprietary assets into the implementation.
  Document behavior in an implementation-neutral specification first.
- This project is explicitly allowed to publish exact protocol behavior, storage
  and cryptographic recipes, necessary constants, synthetic vectors, and
  clean-room implementations when they help authorized users run KakaoTalk through
  another client or bridge. Do not withhold useful interoperability findings merely
  because the same mechanism protects authentication or session state.
- Credential or session import/recovery tooling is allowed for operator-owned or
  explicitly authorized profiles. Make the action explicit, scope it to a
  user-selected profile, use exact version guards, avoid logging recovered values,
  and fail closed. Do not add stealth collection, bulk harvesting, arbitrary
  third-party targeting, or transmission to unrelated endpoints. Using recovered
  material in the documented Kakao authentication flow is allowed when the
  operator explicitly configures it.
- Publication safety means removing live secrets and identifying artifacts—not
  obscuring reproducible algorithms. Prefer synthetic fixtures and clear warnings
  about the sensitivity and portability of recovered material.

## Code conventions

- Use Go for production code and small scripts unless a specialized research tool
  requires another language.
- Keep the protocol core independent from Matrix/Beeper and from storage/UI.
- Put executables under `cmd/`, reusable public packages under `pkg/` only when a
  stable public API exists, and private code under `internal/`.
- Write tests for protocol parsers and state machines using synthetic or thoroughly
  sanitized fixtures.
- Whenever a bug is discovered in production or during a live owned-account
  experiment, add a regression test that reproduces the failure and protects the
  fix before considering the bug resolved.
- Use the repository's default pure-Go Olm backend (`goolm`), matching CI.
  Run `make check` (race-enabled tests, vet, formatting, linting, vulnerability
  checks) and secret scanning before merging. For direct Go commands, including
  focused tests and `go run`, set `GOFLAGS='-tags=goolm'`; untagged builds require
  C libolm development headers. Do not change user-wide Go defaults to set this
  repository's build tag.

## External actions

- Installing local research tools and inspecting the logged-out KakaoTalk client
  are authorized for this project.
- Direct ADB control of maintainer-owned Android lab emulators is authorized for
  this project and does not require separate confirmation for each action. Agents
  may use ADB for app launch/navigation, taps and text entry, screenshots and UI
  dumps, file push/pull, QR import, and owned-account approval flows. Use ADB when
  the native computer-control interface cannot see the emulator. Keep any account,
  message, QR, verification-code, or device data outside the repository and remove
  transient captures after the experiment. This standing authorization does not
  extend to an unlisted physical device or another person's account.
- Account creation/login may require maintainer interaction for phone verification,
  CAPTCHA, MFA, or accepting service terms. Never record those secrets in project
  files or commentary.
- Publishing sanitized source and documentation to `frrad/mooo` is authorized.
