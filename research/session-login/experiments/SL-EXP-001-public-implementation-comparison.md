# SL-EXP-001 — public implementation comparison

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: public sources pinned in method
- Public artifact ID: SL-PUB-001
- Private artifact reference: sanitized public-source comparison report
- Evidence class: public prior art

## Question

Which bootstrap, LOGINLIST, reconnect, and kickout claims are independently
corroborated by public source, and where do current and historical implementations
conflict?

## Authorization and safety boundary

Only public repositories and official public product material were inspected. No
client binary, account, credential, traffic, or leaked source participated.

## Hypothesis

Public implementations agree on the three-stage bootstrap and core LOGINLIST shape,
but their fixed values, resume behavior, and historical registration APIs are too
inconsistent to serve as a current specification.

## Method

Compare OpenKakao revision `336cc9147303ed6e9b1a7c2cb39545327bffd5af`,
node-kakao revision `8512e4699e2cd4c26516b80d38b57b2f85a6cc8a`, and
Kakao's official 2013 Windows launch description. Inspect implementation code, not
only rendered examples or changelog claims.

## Sanitized observation

Both implementations use `GETCONF -> CHECKIN -> LOGINLIST` and the source code of
both spells the token field `oauthToken`. Their LOGINLIST models agree on most
field names and BSON types. They conflict on `rp`, revision behavior, MCC/MNC fixed
values, and some bootstrap fields.

Both reconnect through a new bootstrap/login sequence. The current public client's
saved resume state is not wired into its reconnect LOGINLIST or a catch-up request
at the pinned revision, so its resume claim is not protocol evidence. `CHANGESVR`
and `KICKOUT` are separate events. Historical kickout reason labels lack current
corroboration.

Historical form routes and verification headers conflict with the current macOS
registration route family and must not be transplanted.

## Conclusion

The hypothesis is supported. Public evidence is useful for candidate fixtures and
negative compatibility tests, but current-client static evidence remains normative.

## Cleanup

No private or runtime artifacts were created.

## Follow-up

Use current-client static and synthetic serializer evidence to settle every public
conflict before implementing a fixed profile.
