# Short encrypted-frame decrypt boundary

This note records an account-free Foundation probe for the slice arithmetic
used by the KakaoTalk 26.8.0 arm64 decrypt path. It is a platform-specific
boundary experiment, not a claim about live KakaoTalk exception handling.

The observed source path splits an input of length `N` into an IV beginning at
offset 0 with length 12, ciphertext beginning at offset 12 with length
`N - 28`, and a tag beginning at offset `N - 16` with length 16. The source
performs these `NSData initWithBytes:length:` calls without a local length
check, clamp, or exception handler. Its caller likewise has no observed local
handler around the slice construction.

The synthetic probe ran on macOS 26.6.2 (build 25G83), arm64, using the system
Foundation framework. Each case ran in a forked child with a 128 MiB address
space limit, a two-second alarm, and Objective-C exception capture. The input
was either nil or synthetic bytes; no account, message, credential, or Kakao
runtime state was used.

| Input length `N` | Synthetic nonnil NSData | Nil NSData |
| ---: | --- | --- |
| 0 | SIGSEGV (11) | SIGSEGV (11) |
| 1 | `NSInvalidArgumentException` while allocating the underflow-sized ciphertext | SIGSEGV (11) during the initial 12-byte IV construction |
| 12 | `NSInvalidArgumentException` while allocating the underflow-sized ciphertext | SIGSEGV (11) during the initial 12-byte IV construction |
| 21 | `NSInvalidArgumentException` while allocating the underflow-sized ciphertext | SIGSEGV (11) during the initial 12-byte IV construction |
| 27 | `NSInvalidArgumentException` while allocating the underflow-sized ciphertext | SIGSEGV (11) during the initial 12-byte IV construction |
| 28 | IV 12 bytes, ciphertext 0 bytes, tag 16 bytes | SIGSEGV (nil IV source) |
| 29 | IV 12 bytes, ciphertext 1 byte, tag 16 bytes | SIGSEGV (nil IV source) |
| 44 | IV 12 bytes, ciphertext 16 bytes, tag 16 bytes | SIGSEGV (nil IV source) |

The underflowed lengths observed for `N=1,12,21,27` were the unsigned values
`18446744073709551589`, `18446744073709551600`, `18446744073709551609`, and
`18446744073709551615`. The probe's Foundation allocator rejected those
requests under the resource cap. The signal results come from the synthetic
nil/zero-length pointer path and should not be generalized to another
Foundation release, architecture, allocator, or caller context.

The reproducible implementation boundary for a clean-room client is therefore
`N >= 28` for the three-slice operation. Inputs below that bound need an
explicit policy in the caller; this experiment does not establish whether the
official client catches, terminates on, or prevents those inputs before the
helper is reached. The private probe source and raw output remain outside the
repository.

## Source provenance and scope

The slice helper is `LocoV2SLCrypto`'s `decrypt:` implementation at
`0x101685970`. The selected receive consumer is `didReadBody:` at
`0x101773aa0`; its decrypt call is at `0x101773acc`, followed by the
unconditional `supplyRawData:` call. The source resolves the crypto operation
as `decryptAES128GCMWithKey:iv:aad:tag:`. These addresses and selector mappings
are cross-checked in the merged [V2SL receive/decrypt contract](V2SL-RECEIVE-CONTRACT.md)
and [secure framing contract](SECURE-FRAMING.md).

`N >= 28` is only the clean-room precondition that makes the three source
slices representable. It does not verify the authentication tag, establish a
successful GCM decrypt, or prove that the caller accepts the resulting frame.
Tag verification and decrypt-result handling remain separate source contracts.
