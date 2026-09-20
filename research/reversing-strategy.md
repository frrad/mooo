# Reversing strategy

Status: initial target decision, 2026-09-20.

This document selects the first official client to study and defines the boundary
between private binary analysis and public protocol work. It records conclusions,
not decompiler output. Raw artifacts and account-specific observations remain in
the ignored lab area described by `research/CLEANROOM.md`.

## Decision

Use the current macOS client as the primary behavioral target. Use the Android
tablet mode as a comparative source for authentication states and field meanings.
Acquire and study the current Windows client later when an independent comparison
would resolve an ambiguity.

The intended bridge should present itself as an approved secondary device. Desktop
clients are the least ambiguous match for that role, and macOS gives this project
the shortest path from an official executable to repeatable analysis on the
existing host.

## Platform assessment

| Platform | Secondary-device evidence | Analysis fit | Role |
| --- | --- | --- | --- |
| macOS | Official desktop client with device verification; actively supported | Current universal binary is locally available, statically inspectable, and runnable on the research host | Primary target |
| Windows | Official desktop client with device verification | Strong comparison target, but dynamic work adds a Windows host or VM and another artifact pipeline | Later differential target |
| Android tablet | Current package contains an explicit tablet configuration and secondary-device login states | Excellent static comparison material, but split APKs, obfuscation, native protection components, and emulator classification add noise | Authentication oracle and cross-check |
| iPad | Official iPad application exists; current independent-device semantics were not found in official documentation | Apple signing and device requirements add friction without a demonstrated protocol advantage | Defer |
| Chromebook | Official listing supports Chromebook; its device-class and concurrency semantics are undocumented | Likely overlaps Android analysis without resolving whether it is treated as a desktop or tablet | Defer |
| Watches | Listings describe limited companion features; independent account sessions were not found | Not a demonstrated full account/session target | Out of scope |

Current product claims are grounded in Kakao's [Windows launch
notice](https://www.kakaocorp.com/page/detail/7587), [desktop support
notices](https://pc.kakao.com/talk/notices/en), [Mac minimum-version
notice](https://pc.kakao.com/talk/notices/en/2987?agent=mac), [Google Play
listing](https://play.google.com/store/apps/details?id=com.kakao.talk), and
[Apple App Store listing](https://apps.apple.com/us/app/kakaotalk-messenger/id362057947?platform=ipad).
Exact concurrency limits and tablet approval behavior remain questions to verify,
not assumptions in the implementation.

Kakao's [automatic-detection
policy](https://talksafety.kakao.com/en/measure) identifies virtual international
numbers and computer emulators as possible protection triggers. Account-backed
experiments therefore come after offline analysis, use a disposable authorized
account, and avoid repeated speculative login attempts.

## Initial binary assessment

The inventoried macOS 26.8.0 application is a signed universal Mach-O containing
arm64 and x86_64 code. The installed executable does not advertise Mach-O binary
encryption, packing was not observed, and release metadata retains substantial
Objective-C and Swift type and method information. Its bundled components and
imports show separate surfaces for HTTP, long-lived sockets, serialization,
cryptography, keychain access, and encrypted local storage.

An offline string and symbol survey supports this working decomposition:

1. an HTTPS account and device-registration layer;
2. a booking/configuration step that selects a messaging endpoint;
3. a persistent LOCO session with framed commands;
4. keychain-backed device/session material and an encrypted local database.

This is an architecture hypothesis, not yet a protocol specification. Component
presence does not establish which algorithm, endpoint, or storage path is used for
a particular operation.

The logged-out application can be launched normally. A read-only Frida attachment
attempt from the research user's normal session was rejected by macOS process
access controls. The project will not weaken host security controls merely to make
instrumentation convenient. Static analysis remains viable; later dynamic work
can use documented logging, an explicitly prepared disposable environment, or
other authorized observation points with a cleanup plan.

## Work plan

### 1. Authentication state machine

- Trace the request construction and response handling for device-code generation,
  approval polling, registration, cancellation, logout, and revocation.
- Describe inputs by meaning and encoding rather than copying client types or code.
- Map server results into stable states, retry rules, expiry behavior, and user
  actions.
- Compare the result with Android tablet's explicit secondary-device states.

Deliverable: an implementation-neutral state diagram and synthetic transition
tests. Account access is not required for the first static draft.

### 2. Session bootstrap

- Recover the order and purpose of configuration, booking, check-in, and session
  login operations.
- Identify endpoint-selection inputs, client-version gates, device-class fields,
  and reconnect behavior.
- Locate framing, serialization, compression, and cryptographic boundaries without
  recording live keys or payloads.

Deliverable: a byte-level framing specification using invented field values and
synthetic fixtures.

### 3. Credential lifecycle

- Determine which device credentials are generated locally versus returned by the
  service.
- Identify keychain and database ownership boundaries, renewal behavior, logout,
  and remote revocation semantics.
- Define a storage interface suitable for a headless Linux service without copying
  platform-specific secret material.

Deliverable: a credential lifecycle specification and threat model.

### 4. Minimum messaging surface

- Trace session resume, contact/chat discovery, message receive, acknowledgements,
  and text send in that order.
- Defer media, calls, payments, commerce, and other unrelated surfaces.
- Confirm ambiguous behavior against a disposable account only after a bounded
  experiment and explicit sanitization plan exist.

Deliverable: a narrow conformance model sufficient for a read-only Go client,
followed by text send.

## Decision gates

Start the Go protocol implementation only when all of these are true for one
vertical slice:

- the observed behavior is expressed without proprietary code or internal offsets;
- field meanings and state transitions have a confidence rating and provenance;
- examples use synthetic values;
- an independent implementation agent can work from the specification alone;
- unresolved security properties are called out rather than guessed.

Revisit the platform choice if static macOS analysis cannot identify the session
framing boundary, if current Windows behavior materially differs, or if Android
tablet proves to be a simpler and equally faithful secondary-device identity.

## Immediate questions

- What exact response completes desktop device registration, and which returned
  material survives restart?
- How are booking and check-in messages framed and associated with a device class?
- Is application-layer encryption applied to every persistent-session command or
  only selected payloads?
- Which client-version, locale, network, and device fields are policy inputs versus
  telemetry?
- What observable event distinguishes revocation, expiry, concurrent-device
  displacement, and account-protection blocking?
