# Incoming typed push notices

Status: reviewed static source and framework behavior; runtime delivery and
outer exception handling remain unexecuted. Client build: macOS KakaoTalk
26.8.0. The synthetic contract is in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-incoming-push-notices.json`.

## Observed chain

The packet producer (`0x101774064`) hands a recognized method to the default
receive handler (`0x10152049c`). The handler chooses the notice class from its
method map, calls `initWithPacket:` (`0x10166b338`), and only then invokes the
notice-specific delegate and receipt path. `initWithPacket:` reads
`packet.body` and passes it to the model's `initWithJSONObject:`.

`LocoHintPushNotice` and `LocoBlockSyncPushNotice` inherit that packet
initializer from `LocoPushNotice` (`0x1021af070`). HINT overrides
`initWithJSONObject:` (`0x1016fcdfc`): it calls the superclass first, then
constructs `LocoChatLog` with the same JSON object and assigns it to `chatLog`.
Thus a nil body reaches the superclass before nested-chat-log construction.
The bundled SGJsonObject initializer (`SGJsonKit+0x350c`) raises
`NSInternalInconsistencyException` for nil input. The synthetic nil case
therefore stops before nested construction, delegate delivery, or receipt
construction. No outer catch, disconnect, or retry policy is claimed.

A nonnil empty NSDictionary follows the normal property walk. Missing keys and
NSNull values are skipped, leaving Objective-C defaults in place. This makes an
empty dictionary distinct from a nil body. The HINT nested chat-log initializer
receives the same dictionary object; no separate body or synthesized fields are
introduced by this contract.

The static receive handlers call the optional delegate first. They then build
the corresponding push receipt and call the carriage receipt sender. The
synthetic effects record this order for HINT and BLOCKSYNC; runtime receipt
transport is intentionally not enabled here.

## BLOCKSYNC input mapping

`LocoBlockSyncPushNotice`'s `nameMappingDictionary` implementation is
`0x101350748`. The constant dictionary contains compact incoming keys as its
keys and Objective-C property names as its values:

| incoming source | destination property |
| --- | --- |
| `r` | `revision` |
| `pr` | `plusRevision` |
| `f` | `isFull` |
| `l` | `blockIds` |
| `ts` | `blockTypes` |
| `dl` | `unblockIds` |
| `pf` | `plusIsFull` |
| `pl` | `plusBlockIds` |
| `pts` | `plusBlockTypes` |
| `pdl` | `plusUnblockIds` |

The model mapping block receives each source/destination pair and performs a
source lookup, destination assignment, and source-key deletion for a present
ordinary value. Missing sources are skipped; the NSNull branch is not treated
as an ordinary value. In particular, `r`/`pr` are incoming source keys and are
removed after assigning `revision`/`plusRevision`. This direction is derived
from the notice initializer's source mapping dictionary and is independent of
the outgoing receipt mapping.

The reviewed notice metadata declares `revision` and `plusRevision` as signed
`int32`. Vectors use explicit signed int32 values, including both boundaries;
Foundation KVC numeric coercion for other input object types is untraced and is
not specified here. The outer exception policy around model construction and
handler delivery is also untraced.

## Scope and gaps

This document records source behavior and synthetic effects only. It does not
activate a default notice dispatcher, receipt sender, pending-request
correlation, encryption, or reconnect policy. The fixture does not claim BSON
wire behavior or Foundation coercion beyond the explicit int32 inputs.
