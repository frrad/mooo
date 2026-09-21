# Repository instructions

## Mission

Build a public, self-hostable KakaoTalk secondary-device implementation in Go,
followed by a Matrix/Beeper bridge. Prioritize protocol understanding and safe,
reproducible research before bridge features.

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
- Make small, reviewable commits. Pull requests use squash merges and require
  passing CI before merge.
- The maintainer authorizes agents to merge their own pull requests once all
  required CI checks pass and GitHub reports the PR as mergeable. Always use a
  squash merge; never merge with pending or failing required checks.

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
- Run `go test ./...`, `go vet ./...`, formatting, linting, vulnerability checks,
  and secret scanning before merging.

## External actions

- Installing local research tools and inspecting the logged-out KakaoTalk client
  are authorized for this project.
- Account creation/login may require maintainer interaction for phone verification,
  CAPTCHA, MFA, or accepting service terms. Never record those secrets in project
  files or commentary.
- Publishing sanitized source and documentation to `frrad/mooo` is authorized.
