# DR-EXP-003 — registration-to-session handoff

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: DR-ART-MAC-ARM64-001
- Private artifact reference: targeted handoff/persistence Ghidra report and queries
- Evidence class: static

## Question

What happens after secondary-device approval, which state crosses into normal LOCO
login, what persists, and how does logout unregister the current device?

## Authorization and safety boundary

The analysis was offline against an authorized binary. It did not launch a client,
read preferences or live identifiers, access an account, or contact a server.

## Hypothesis

Permanent and temporary registration converge on a common session-login boundary,
while persistent auto-login state is stored separately from transient QR values.

## Method

Trace registration success callbacks through restore eligibility, temporary-device
state, the common login coordinator, auto-login storage calls, logout/reset cleanup,
and directly attributable unregister and kickout handlers. Cross-check storage
behavior against the independently reviewed credential-storage specification.

## Sanitized observation

Temporary QR success skips restore, marks the session as temporary, and enters the
common login coordinator. Permanent success checks restore eligibility and either
enters backup/restore or proceeds directly; restore completion or skip converges on
the same coordinator. Transient QR/device-auth values are cleared first.

The downstream login consumes at least user identity, access token, and background
state, but its final LOCO `LOGIN` packet remains unresolved. Returned auto-login
material uses the documented device-bound encrypted-preference boundary. Logout
and reset remove active and cleanup-only authentication preferences.

The route `/mac/account/destroy.json` is directly attributable to unregistering
the current Mac device during logout, not deleting the account. No route for
listing or revoking other devices was attributable. Server kickout handling exists,
but its numeric reasons remain unmapped.

## Conclusion

The hypothesis is supported. Registration, restore, storage, session bootstrap,
and unregister are distinct interfaces. The final LOCO schema, persistence flag
truth table, unregister body/response, other-device management, and kickout reasons
remain explicit blockers.

## Cleanup

Raw Ghidra output stayed in private lab storage. No application, account, network,
or preference state was changed.

## Follow-up

Trace the final carriage-login encoder and validate persistence flags with invented
state before any future single disposable-account experiment.
