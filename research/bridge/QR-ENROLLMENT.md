# Bridge QR enrollment evidence

## Structural transport observation

On 2026-10-04 America/Los_Angeles (2026-10-05 UTC), a clean-room Go probe
used a fresh anonymous, client-owned identity and the reviewed Mac request
profile (`mac/26.8.0/en`, `Mc/26.6.2`, English). It performed one generation
request followed immediately by cancellation. No QR was scanned, no approval
or poll was performed, and no credentials were returned or installed. The
temporary identity profile was removed after the probe.

The generation response was HTTP 200 with protocol status 0. Its QR value was
a relative URL: scheme and host were empty, the path had four segments, and
the only query field was one `id` value. Cancellation also returned HTTP 200.
The probe retained no QR payload, identifier, or raw response body.

This establishes the bridge's relative-URL acceptance policy and request
lifecycle only. The exact observed path is intentionally not retained, the
official Mac check-key algorithm remains unresolved, and full bridge
enrollment (approval, device authorization, credential persistence, and
restart resume) still requires live validation. The probe used an authorized
owned lab environment and did not import official-client credentials.

The second controlled observation (2026-10-04 America/Los_Angeles) used the
owned B Android AVD, a fresh bridge profile directory, and the disposable
Matrix homeserver. The QR image was created about 0.7 seconds after the
`login qr` command; its authenticated Matrix media replay decoded successfully
with ZXing and matched the Matrix event body exactly. Importing that image
through KakaoTalk's Album scanner produced the generic “You cannot use this
QR code” modal. The bridge received no approval, device-authorization code, or
successful poll transition and eventually expired the challenge. The fresh
profile, temporary image, and Android copy were removed afterward; no
credentials were installed.

The bridge treats QR cancellation as fail closed: an HTTP 200 with an empty
body or explicit status zero is accepted; a nonzero or malformed status body
is rejected. This is an implementation safety policy, not a claim that the
full official cancellation response contract has been recovered.

### Successful clean-room runner comparison

The private clean-room runner used for the 2026-09-28 successful Mac QR flow
is `/Users/frederick/Projects/mooo/.lab/qr_login_probe.go`; its source and
invocation records remain outside the repository. Static comparison shows it
loads an existing snapshot identity, derives the wire UUID from that identity,
uses `BuildQRGenerateRequest`, the same Mac header profile, a 15-second HTTP
client timeout, a 90-second outer context, and three-second polling. It does
not add a check-key field, override, or imported official credential. The QR
image was produced by the private CoreImage renderer with its own correction
and scaling settings, then the complete server payload was passed unchanged.

Presence-only inspection of the referenced private state shows the normal
authstate shape with identity metadata and a credential section; the runner
reads only `Snapshot().Identity` before generation and does not pass recovered
credential material into the request. This establishes an existing authstate
snapshot as the identity source without claiming that it was an imported
official-client profile.

Bridgev2 uses a newly created client-owned identity, the same request builder
and header construction, a 30-second HTTP client timeout, challenge-derived
deadline handling, and the same three-second poll cadence. Its display step
uses the framework QR renderer rather than the private CoreImage command.
Therefore the concrete remaining runtime differential is renderer/configuration
and identity lifecycle (existing snapshot versus fresh profile), not a missing
check-key request parameter. The private runner's successful persistence path
also writes credentials through `authstate` before subsequent session use;
bridgev2 performs its equivalent persistence only after the typed success
handoff. No check-key getter invocation or field override appears in the
successful runner source.

The bridgev2 command lifecycle was checked against the pinned mautrix
`doLoginDisplayAndWait` implementation. It renders the supplied payload with
`go-qrcode` at Low correction and 512px, uploads it, then calls `Wait` with a
child context. A media-send failure calls `login.Cancel`; normal command
cancellation cancels that child context. On a successful step change, the
command redacts the prior QR event before advancing. This ordering differs
from the private runner's local PNG write, but no lifecycle defect or extra
request/credential input was found in the framework path.

### Mac check-key boundary (static correction)

A fresh Mach-O metadata pass on the owned macOS 26.8.0 binary resolves the
`qrLoginCheckKey` selector to the `FCAuthController` instance method with
Objective-C type `@16@0:8`. Its method body conditionally bridges Swift
`Foundation.Data` to `NSData` and returns an autoreleased object. The earlier
description of this as a mistaken selector-to-IMP mapping was incorrect: the
Swift data helper is the method body boundary itself. Static references do not
show a direct caller, so Swift direct dispatch or runtime selector dispatch
remains possible.

The static trace still does not establish what bytes populate the returned
`Data`, whether they derive from the device/challenge state, or where the
getter feeds QR generation or polling. The route-specific QR-generate parser
contains no `checkKey` dictionary lookup, and the authorized debug run reached
generation without entering this getter. No check-key algorithm or input
recipe is therefore transferred into the bridge.

The helper body itself performs additional object lookups through an owned
authentication/configuration object, conditionally extracts a string-like
value, constructs an Objective-C string, applies another object operation, and
bridges the resulting `NSData` back to Swift `Data`. The available stripped
metadata does not resolve those dynamic selectors or identify the backing
field, so this narrows the boundary to object-backed state without proving
whether that state is a device secret, challenge-derived value, or cached
configuration. The QR URL parser's `id` extraction remains a separate,
observable path and is not evidence that it supplies this getter.

## Presentation boundary and current live gap

The connector preserves the complete server QR string in the bridgev2 display
step. This matches the clean-room macOS renderer trace, which supplies that
string unchanged to its QR generator, and the Android trace, which locates the
account-info path and extracts the raw `id` suffix without requiring a scheme or
host. The shared bridgev2 command currently owns PNG generation (including its
error-correction and raster settings); the connector has no image-rendering
hook and must not prepend an unproven host or rewrite the challenge.

The controlled presentation observation was rejected by the owned Android
client with the modal above. A private replay of the actual rejected Matrix
image (retrieved from the disposable homeserver and deleted after analysis)
decoded successfully with ZXing and matched the original Matrix event body
byte-for-byte. This rules out media corruption, PNG transport, and raw-payload
mutation for that attempt. The modal/source trace assigns the failure to the
scanner's QR-info `GENERAL_NOT_FOUND` branch; the remaining cause is the
server-side reason for that response, which is not exposed by the sanitized
observation. The bridgev2 renderer is supplied by the framework rather than
the connector.

The observed modal can now be assigned to a concrete Android branch from the
offline source audit. The scanner's QR item posts the invalid-message event
only when its `qrCodeLogin/info` call raises a `GENERAL_NOT_FOUND`
`TalkStatusException`. The active scanner fragment receives that event and
constructs the modal dialog containing “You cannot use this QR code.” The same
string is also used by the separate sub-device QR display fragment for its
inline invalid state and accessibility description, so the string resource by
itself is ambiguous; the modal presentation identifies the scanner event path.
This proves that the observed attempt reached the scanner's QR-info error
handling, subject to the source audit's clean-room interpretation. It does not
expose the request URL, server response body, account policy cause, or prove
that the bridge challenge would be accepted after a different presentation.

### Official Android presentation and scan path (source audit)

An offline audit of the owned Android 26.8.2 APK traced the relevant chain
without retaining account values or proprietary source. The scanner accepts a
decoded string containing `/talk/account/qrCodeLogin/info.json`, extracts the
literal substring after `/talk/account/qrCodeLogin/info.json?id=`, and sends
that value to `android/account/qrCodeLogin/info`. A successful info response
routes to the QR-login approval screen; a general-not-found response reports
the invalid-QR state, while other server failures surface a service message.
The official QR display path passes the server URL directly to a ZXing QR
writer configured with error correction `H`, zero quiet-zone margin, and a
150dp square bitmap. The bridgev2 command path currently renders QR values
with `go-qrcode` at error correction `Low` and a 512px image. This is a
concrete renderer-parity difference to test offline; it is not yet evidence
that `Low` caused the Android rejection: the recovered failed image decoded
correctly and preserved its payload. The current bridge test therefore
verifies raw-payload preservation while the post-decode rejection path remains
pending controlled info-response observation.
