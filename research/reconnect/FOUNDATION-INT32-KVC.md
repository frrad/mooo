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
filters `NSNull` before KVC for declared properties, so the `NSNull` probe
does not imply a whole-chain failure when it arrives through that projection
path. Array and dictionary handling in the outer incoming chain remains
untraced.

The official incoming model metadata identifies
`LocoBlockSyncPushNotice.revision` and `plusRevision` as signed 32-bit
properties (`Ti`, width 4), with ivars of the same type. Its
`initWithJSONObject:`
path reads dictionary values and routes the typed values through
`setValue:forKey:`. The SGJsonObject null guard skips `NSNull` before that KVC
write; the outer behavior for array or dictionary inputs remains untraced.
This note records the conversion boundary only; it does not claim a default
notice activation policy or a server-side acceptance rule.

The reviewed incoming source receipts cover the `SGJsonObject`
`initWithJSONObject:` initializer (SGJsonKit offsets `0x350c` and `0x3648`),
the `LocoBlockSyncPushNotice` mapping implementation at `0x101350748`, and
the `LocoModel` implementation at `0x10167beb0`. Neither the notice nor the app-level
`LocoModel` metadata shows an app override of `setValue:forKey:` or
`setValue:forUndefinedKey:`; framework fallback and outer exception handling
remain separate boundaries. The related incoming notice contract is recorded
in [`INCOMING-PUSH-NOTICES.md`](INCOMING-PUSH-NOTICES.md).

The official binary also supplies the nonstandard NSNumber factories used by
that decoder in the `NSNumber(FIRCLSWrappedReportAction)` category. The class
method at `0x101512340` (`+[NSNumber numberWithInt32:]`) allocates `NSNumber`
and sends `initWithInt:` through stub `0x1018db7e0`; the class method at
`0x101512368` (`+[NSNumber numberWithInt64:]`) allocates `NSNumber` and sends
`initWithLong:` through stub `0x1018dbda0`. The category's
`-[NSNumber int32Value]` (`0x101512338`) forwards to `intValue`, and
`-[NSNumber int64Value]` (`0x10151233c`) forwards to `longValue`. The category
contains no `objCType` override. A private macOS 26.6.2 probe exercising the
same explicit `alloc/initWithInt:` and `alloc/initWithLong:` paths produced
`__NSCFNumber` with Objective-C type `i` for the int32 path and `q` for the
int64 path, with the same 32-bit KVC outcomes as the factory vectors. This connects the decoder's nonstandard selectors to the
platform probe without claiming a proprietary runtime class beyond that
platform observation.

The decoder-to-KVC input classes are also source-grounded. Decoder IMP
`0x1017eb434` calls helper `0x1017eb504`; its BSON `0x10` branch invokes
`NSNumber numberWithInt32:` through stub `0x1018f4420`, while BSON `0x12`
invokes `NSNumber numberWithInt64:` through stub `0x1018f4440`. The app
metadata contains no notice or `LocoModel` override for the KVC setter. The
inspected SGJsonObject base and NSObject SGJsonKit category metadata also show
no `setValue:forKey:` or `setValue:forUndefinedKey:` override. This scopes the
source check to those inspected classes and does not claim that dynamic
registration, swizzling, or other framework/runtime overrides are absent.

The synthetic vector test records the input class and Objective-C type for all
23 cases, including explicit `alloc/initWithInt:` int32 decoder-factory cases
and the seven `alloc/initWithLong:` signed-64-bit cases. It is
`internal/protocol/sessionlogin/foundation_int32_kvc_contract_test.go`, with
inputs in
`research/fixtures/reconnect/rc-q5-foundation-int32-kvc.json`.
The vector test is an implementation-neutral replay of the captured outcomes,
not a replacement for Foundation. The private source provenance is the local
`foundation-int32-kvc/probe.m` and `result.txt` receipt, including the
`NSConstantIntegerNumber`/`__NSCFNumber` and `q`/`d`/`c` input labels, the
Objective-C metadata receipt for `LocoBlockSyncPushNotice`, and the
SGJson/BSON decoder receipts for the incoming notice chain. The incoming
notice source and synthetic mapping vectors are in
[`INCOMING-PUSH-NOTICES.md`](INCOMING-PUSH-NOTICES.md), supplied by the
separate source-contract change.
