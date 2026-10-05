# V2SL receive/decrypt boundary

Status: reviewed static source contract; synthetic tests only. This slice
extends the merged read-header/body routing contract with the secure body
decrypt operation. It does not claim pending-map teardown or connection
replacement behavior.

The `socket:didReadData:withTag:` callback routes tag 1 through
`toggleInSegmentTimeout:false` and `didReadBody:`. Tag 0 routes to
`didReadHeader:`. The secure body path checks `_v2slCrypto` at offset `0x18`.
Without that object, the callback supplies the received data directly through
`supplyRawData:`. With it, `didReadBody:` calls `decrypt:` and supplies the
returned object through the same `supplyRawData:` selector; the callback has no
decrypt-result presence branch before that supply call. A nil authentication
result is therefore represented as an input-derived callback boundary, not an
inferred disconnect or pending failure.

The `LocoV2SLCrypto decrypt:` IMP is `0x101685970`. For an input of length N,
it constructs an IV data object from bytes `[0,12)`, a cipher data object from
offset 12 with length N-28, and a 16-byte tag object from the final 16 bytes.
It reads the crypto object's AES key and IV and calls
`decryptAES128GCMWithKey:iv:aad:tag:` with nil AAD. The synthetic secure fixtures are bounded to N≥28 so the observed slice lengths are representable. The source passes N-28 to NSData without a guard in this slice; behavior for shorter input, including any Foundation range failure, remains untraced and is not presented as an official validation rule.

The synthetic fixture derives the cipher length from the input length and
keeps a failed/nil decrypt result separate from the call and supply effects.
It does not infer authentication error propagation, socket cancellation,
pending completion fanout, or current-connection replacement identity; those
remain explicit gaps requiring their own raw callback chain.

Private provenance (not part of the repository):

- `reconnect-read-handlers/read-callback-decomp.txt` and
  `locoagent-read-body-header-full-raw-otool.txt` cover tag routing and the
  `_v2slCrypto` branch in `didReadBody:`.
- `reconnect-read-handlers/decrypt-query-20261005.txt` maps `decrypt:` to
  `0x101685970`.
- `reconnect-read-handlers/decrypt-v2sl-decompile-20261005.txt` records the
  IV/cipher/tag slices and decrypt call.
- `credential-storage/raw/cs1/objc-stubs-disassembly.txt` resolves the
  `decryptAES128GCMWithKey:iv:aad:tag:` selector stub.

## Selected LocoNWAgent read callback

The selected manager transport is `LocoNWAgent`, whose `readHeader` and
`readBody:` methods call the receive helper at `0x100d48298`. The helper reads
the current `connection` ivar at callback time. A missing owner or connection
returns before scheduling a receive. With a connection it enables the outgoing
segment timeout and calls `NWConnection.receive` with minimum length 1 and
maximum length equal to the requested input length.

For a data completion, the closure disables the incoming segment timeout,
bridges the received Data to NSData, calls `didReadBody:`, then calls
`readHeader`. For an error completion, POSIX code 0x59 skips the log/cancel
branch. Other errors log the read-body failure and cancel the current
connection loaded from the owner at callback time; a replacement connection
therefore changes the cancellation identity. This closure has no observed
pending-map or status publication effect, so those downstream consumers remain
an explicit gap.

Private provenance for this subsection is the sanitized receipt names
`reconnect-conf-model/otool-objc.txt`, `credential-storage/raw/cs1/objc-stubs-disassembly.txt`,
and `nw-readbody-closure-20261005.txt` in the external parity archive.

### Raw nil and short-input boundaries

The raw `didReadV2slData:` body at `0x101773aa0` branches only on the
presence of `_v2slCrypto` at `+0x18`. In the crypto branch it calls
`decrypt:` and then unconditionally calls `supplyRawData:` (`0x101933520`);
the raw sequence at `0x101773ae0`–`0x101773af4` has no result-presence branch.
Thus a nil decrypt return remains a supply call with a nil argument, rather
than an inferred early return or cancellation. Without crypto, the same
selector receives the original data object.

The decrypt IMP at `0x101685970` computes the cipher length as the input
length minus `0x1c` and passes that value directly to the NSData constructor;
there is no observed comparison or branch guarding short input before that
constructor. The public fixtures therefore use N≥28 only to keep the
input-derived slice arithmetic representable. Short-input range behavior is
still an explicit raw-runtime gap, not a claimed protocol validation rule.
