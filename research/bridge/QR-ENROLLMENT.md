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

On 2026-10-04 America/Los_Angeles, one controlled fresh observation used the
owned B Android AVD, a fresh bridge profile directory, and the disposable
Matrix homeserver. The QR image was created about 0.7 seconds after the
`login qr` command; its authenticated Matrix media replay decoded successfully
with ZXing and matched the Matrix event body exactly. Importing that image
through KakaoTalk's Album scanner again showed the generic “You cannot use
this QR code” message. The bridge received no approval, device-authorization
code, or successful poll transition and eventually expired the challenge.
The observation therefore rules out media transport and payload mutation for
this attempt, but does not identify whether Android rejected it during route
classification, the info request, or account-side response handling. The
fresh profile, temporary image, and Android copy were removed afterward; no
credentials were installed.

The bridge treats QR cancellation as fail closed: an HTTP 200 with an empty
body or explicit status zero is accepted; a nonzero or malformed status body
is rejected. This is an implementation safety policy, not a claim that the
full official cancellation response contract has been recovered.

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

The first fresh bridge presentation was rejected by the owned Android client
(`You cannot use this QR code.`). No scan or authorization was retried. The
failure's stage is unresolved: it may be image decoding, scanner route
classification, QR-info lookup, or a later account-side policy response. The
offline APK audit now traces the scanner-to-info request and its broad result
routing. A private replay of the actual rejected Matrix image (retrieved from
the disposable homeserver and deleted after analysis) decoded successfully
with ZXing and matched the original Matrix event body byte-for-byte. This
rules out media corruption, PNG transport, and raw-payload mutation as the
cause of that attempt; the remaining stage is scanner route classification,
QR-info lookup, or a later account-side response. The bridgev2 renderer is
supplied by the framework rather than the connector.

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
