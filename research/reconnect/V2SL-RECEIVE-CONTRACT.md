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
`decryptAES128GCMWithKey:iv:aad:tag:` with nil AAD. The secure body contract
requires N≥28; behavior for shorter input is left outside this bounded slice.

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
