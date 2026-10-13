# SHIP redirect upload investigation

Issue [#342](https://github.com/frrad/mooo/issues/342). Evidence date: 2026-10-13.

## Observed failure and interim behavior

A fresh synthetic PNG sent once through production `Client.SendImage` from an
existing owned disposable secondary profile into a roster-verified owned direct
chat reproduced the operator failure: `SHIP` returned `status: 0`, `rd: true`,
`k: ""`, `vh: ""`, and `p: 0`. BSON types were string/string/int32/bool/int32.
The old decoder rejected `k` before any trailer connection or byte upload.
No automatic retry occurred; the probe shut down and released its profile lease.
The [fixture](../fixtures/photo/observed-ship-redirect.json) contains no account
identifiers, tokens or content. It establishes server behavior, not parity with
the official client's fallback.

mooo now decodes `rd` before validating a trailer endpoint. A true redirect
returns `ShipResponse.Redirect`; the current send path stops with
`media.ErrUploadRedirect` before POST, a byte stream or a message write. The
bridge reports a certain, non-retriable unsupported attachment status saying
that the attachment was not sent. It keeps the existing single-send reservation.
A wrong BSON type for `rd` is rejected; omission defaults to false. Non-redirect
endpoint validation and the trailer send path are retained.

**This is an interim handling fix, not an implementation of the alternate
uploader. Redirected attachments still cannot be delivered.**

## Mac response model and redirect chain

Read-only Ghidra inspection of the authorized Mac 26.8.0 build 2000 binary:

1. The SHIP request carries chat ID, size, type, checksum, extension and extra.
2. Its response model maps `rd` to a Boolean redirect property. Five synthetic
   inputs executed through `LocoShipResponse.initWithJSONTextString:` and its
   getters confirm true/false/default decoding independently of status. See
   [executed model fixture](../fixtures/photo/executed-ship-model.json).
3. The trailer callback checks success before redirect. A successful redirect
   clears completion/progress handlers and invokes completion with
   success=false, redirect=true, error=nil. It does not use the empty endpoint,
   insert a trailer task or update a pending message as failed on that branch.
4. The controller completion removes the operation from its upload map and
   forwards these values to the caller. The Swift trailer completion converts
   redirect to its dedicated upload error; the upload coordinator catches that
   case and starts direct upload. Other trailer errors propagate instead.
5. The direct branch includes Kage and Tenth strategies, resumable append
   metadata, a processing-readiness path, and `LocoWriteMediaRequest` message
   completion. It does not use the trailer COMPLETE chain. The full direct
   request/response/persistence/consumer/failure contract is still incomplete.

The isolated model probe had neither a network entitlement nor shared app
groups. It was killed after execution. The two mandatory original sandbox
preference files were hashed before and after and were unchanged. This does not
claim a whole-profile immutability check or full coordinator parity.

## Direct upload evidence and blocker

The independently traced Mac prepare builder sends POST
`/messaging/files/v1/prepare` with JSON `chatId`, `mediaType` (`image` for this
photo), `size`, and lowercased `extension`. A synthetic execution intercepted
its dispatcher before network use and confirmed those fields, POST, gzip off
and XVC off: [executed request](../fixtures/photo/executed-prepare-request.json).

Two controlled requests against the existing owned direct chat through
`https://talk-pilsner.kakao.com` returned HTTP 200 with keys `url`, `resource`,
`resourceType`, `uploadKey`, `chunkSize`, and `needReady`. In these observations,
the URL was signed HTTPS on `kage.talk.kakao.com`, its path started
`/offset-up/`, resource type was `vk`, chunk size was 4 MiB, and `needReady`
was false. Live tickets and URLs remain private.

The Mac Kage request/uploader was executed offline with a synthetic URL and
synthetic bytes. Its serialized request was POST multipart with an `offset`
text part and a `file_1` data part carrying filename and MIME type. It applied
A, User-Agent, Accept-Language and Accept-Encoding headers. This covers the
HTTP request construction, not the complete upload job.

- One small fresh PNG transfer using the traced multipart form returned HTTP
  404 with `msg: "Not found upload ready key."`. The signed URL had not expired.
- A separate fresh ticket and PNG with the Mac header family also returned
  HTTP 404. Neither mutation was automatically retried.
- A controlled append-metadata request for that failed resource returned HTTP
  200, the same signed URL, `offset: 0`, 4 MiB chunk size and `needReady: false`.
  No chat-message write was made.
- A separate fresh ticket from the same disposable account was passed to the
  official Mac Kage request/uploader in an isolated signed copy without shared
  app groups or an account profile. Its HTTP completion also reported 404.
  The ticket was prepared by mooo, so this is an uploader control, not an
  official end-to-end send or proof that the prepare/authentication context is
  correct. Unrelated NSURLSession requests were redirected to loopback; the
  child was killed. This control captured the HTTP completion callback but
  did not retain the response body or trace the complete media job.

The cause of this initialization/authorization/ready-key failure is unknown.
No account-policy explanation is established. No successful Kage upload or
recipient delivery has been demonstrated, and no speculative HTTP fallback is
shipped. Rhino was excluded from reproduction and implementation work.

## Remaining work

- Trace the complete authenticated prepare transport and server initialization
  context; establish why the ready key is missing in this lab path.
- Trace ready polling, Kage chunk continuation/finalization and Tenth responses.
- Finish `LocoWriteMediaRequest`/response serialization, callbacks, persistent
  pending-message replacement and downstream/failure consumers.
- Obtain a successful controlled upload/message completion fixture, then write
  a failing production send-path regression and implement the evidenced route.
- Verify exact owned-recipient delivery, captions, no duplicates and failure
  certainty before calling #342 fixed.
