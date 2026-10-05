# Foundation signed-int32 KVC boundary

Status: platform-bounded synthetic contract. Observation date: 2026-10-04.
The probe ran on macOS 26.6.2 (25G83), arm64, against an `NSObject` synthetic
property declared as `int32_t revision` (`Ti`). It did not inspect an account,
server, or live Kakao session, and it does not establish a universal Apple
Foundation policy.

The local Foundation probe assigned boxed values with `setValue:forKey:` and
recorded the resulting signed 32-bit property. The integer boundary inputs
were signed 64-bit `NSNumber` values (`objCType` `q`) from both the constant
literal class and an explicit `numberWithLongLong:` factory (`__NSCFNumber`).
Both input classes produced the same result and preserve the low 32 bits with
signed interpretation: `2147483648` becomes
`-2147483648`, `-2147483649` becomes `2147483647`, `4294967296` becomes `0`,
`INT64_MAX` becomes `-1`, and `INT64_MIN` becomes `0`. A boxed `double` value
`3.75` becomes `3`, and boxed `YES` becomes `1`. In the same probe, the string
`"2147483648"` became `INT32_MAX` and a nonnumeric string became `0`.

Assigning `NSNull`, an array, or a dictionary raised
`NSInvalidArgumentException` and left the property at its prior value (`7`).
This exception behavior is a direct KVC result. SGJson projection separately
filters `NSNull` before KVC for declared properties, so these object inputs do
not imply a whole-chain failure when they arrive through that projection path.

The official model metadata identifies `LocoBlockSyncPushReceipt.revision`
and `plusRevision` as signed 32-bit properties (`Ti`, width 4), with ivars of
the same type. The SGJson path enumerates declared properties, reads each
value by KVC, then passes the resulting value to the scalar BSON conversion
branch. This note records the conversion boundary only; it does not claim a
default HINT/BLOCKSYNC activation policy or a server-side acceptance rule.

The synthetic vector test records the input class and Objective-C type for all
21 cases, including the seven factory-created signed-64-bit inputs. It is
`internal/protocol/sessionlogin/foundation_int32_kvc_contract_test.go`, with
inputs in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-foundation-int32-kvc.json`.
The vector test is an implementation-neutral replay of the captured outcomes,
not a replacement for Foundation. The private source provenance is the local
`foundation-int32-kvc/probe.m` and `result.txt` receipt, including the
`NSConstantIntegerNumber`/`__NSCFNumber` and `q`/`d`/`c` input labels, the Objective-C
metadata receipt for `LocoBlockSyncPushReceipt`, and the SGJson/BSON static
receipts already listed in `PUSH-RECEIPT-NW-SERIALIZATION.md`.
