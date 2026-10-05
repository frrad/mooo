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

The bridge treats QR cancellation as fail closed: an HTTP 200 with an empty
body or explicit status zero is accepted; a nonzero or malformed status body
is rejected. This is an implementation safety policy, not a claim that the
full official cancellation response contract has been recovered.
