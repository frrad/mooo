# Ordinary audio messages: research in progress

Research date: 2026-10-08. Authorized Android KakaoTalk 26.8.2 APK,
classes10.dex; DEX class definition source-file mapping identifies
`com.kakao.talk.db.model.chatlog.c` as `AudioChatLog.kt`. JADX 1.5.6
single-class inspection is the source for the static observations below.
No public implementation was consulted. No proprietary source is included.

## Static observations, not payload parity

The audio chat-log model exposes a relay resource key and a local-file URI.
Its size getter reads `s`, falling back to `size_3gp` when `s` is nonpositive.
Its ordinary expiry getter reads attachment `expire` in milliseconds; absent
attachment uses sent-at plus the configured content-expiry interval, converted
to milliseconds. Chat category and drawer entitlement affect the official
expired-content decision. These branches require controlled observations before
claiming behavior for all audio resources.

The official display name uses a timestamp formatted in Asia/Seoul and, when
available, the extension derived from a valid relay key. The model reports no
thumbnail and no high-quality variant. It participates in the file-view drawer
consumer. These findings do not yet establish codec, duration, MIME, URL,
checksum, upload behavior or Matrix representation.

## Required acceptance and remaining trace

Trace recording/upload callbacks, received attachment and relay download,
local-file persistence/cache, playback consumers and failure behavior. Capture
only synthetic audio through owned A/B emulators, then validate encrypted native
Matrix media and metadata, retained-key decryption and restart uniqueness.
Untraced layers remain gaps. The scoped direct-URL M4A decoder is in progress; legacy and relay forms remain
unsupported. Naming the enum alone does not establish parity.

## First owned-account observation

A stopped voice memo was sent once to B. The stopped modal hides the underlying
chat accessibility tree; verify recipient before opening it, then require the
stopped preview controls before selecting Send. A scoped secondary MSG capture
observed type 5 and attachment keys `url`, `expire`, `k`, `d`, `s`, with no `cs`.
The direct HTTPS resource downloaded to exactly `s=110298` bytes. Independent
ffprobe inspection identified AAC mono at 16000 Hz in an ISO BMFF M4A container.
The attachment duration was `d=51000` milliseconds; container duration was
51.328 seconds. Expiry was a 13-digit millisecond value. Do not reuse video's
seconds conversion or require its checksum field for this observed audio form.

The intended short recording grew to 51 seconds while UI dump waited for the
animated recorder to become idle. An observed screenshot located Stop, after
which ordinary UI dumps succeeded. Do not call the idle-waiting dump while
recording in a reusable harness: preserve the validated control position and
stop the same recording before dumping. The emulator process did not enable
`-allow-host-audio`; emulator help states the default zeroes microphone input.
This observation establishes one direct-resource form; legacy formats and relay
variants remain unverified. The first captured message passed encrypted native Matrix `m.audio` delivery.
The retained SDK device decrypted the event and verified exact independently
downloaded bytes, filename `audio.m4a`, MIME `audio/mp4`, size and the unscaled
51000-millisecond duration. A fresh shared-script recording also passed encrypted SDK checks against its own
8181-byte resource and `d=2000` milliseconds. The script requested a three-second
wall interval; the received attachment reports two seconds, so expectations use
each received resource rather than the requested recording interval. The
official B client showed both cards and played the earlier recording with
advancing progress. A normal bridge restart retained two unique audio identities.
A later synthetic text arrived once in the bridge and official receiver, and
the earlier audio remained decryptable with retained Matrix keys. Exact
committed-head acceptance remains pending.

## Implemented scope and explicit gaps

The implementation preserves observed M4A bytes as native Matrix `m.audio`, with
`audio/mp4`, filename `audio.m4a`, bounded size, and the unscaled wire duration.
Expiry is checked in milliseconds before fetching. There is no wire checksum in
this observed form: size, allowlisted HTTPS origins/redirects and bounded ISO
BMFF framing are validated, without claiming codec validation or content
authenticity from a nonexistent checksum. Deterministic failures become notices;
transient fetch/upload failures leave the delivery cursor uncommitted.

mooo limits attachment JSON to 64 KiB, media to 64 MiB and duration to 24 hours;
these are implementation policy. Duplicate JSON fields and missing direct URLs
are rejected. Legacy 3GP, relay-only resources, cloud/drawer entitlement expiry,
full uploader callbacks, cache/database persistence, and complete playback
failure paths remain untraced or unvalidated. Outbound audio and actual Matrix
application playback remain unsupported/unverified. Converter tests use explicit
synthetic framing bytes, not an imitation of official codec behavior.
