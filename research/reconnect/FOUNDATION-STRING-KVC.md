# Foundation numeric-string KVC boundary

Status: platform-bounded synthetic characterization. Observation date:
2026-10-04. The captures ran on macOS 26.6.2 (25G83), arm64, against an
`NSObject` synthetic `int32_t` (`Ti`) property. They do not establish a
portable Unicode or numeric-string grammar.

The official scalar decoder's type `0x02` branch constructs an NSString through
`stringWithCString:encoding:` at `0x101932900`. The call loads an NSString class
reference, passes a C-string pointer in `x2`, and passes encoding value `4` in
`x3`; no declared length is passed to this constructor. The decoder first scans
the cursor with `strlen` at `0x1017eb7a8`, then supplies the post-cursor pointer
at `0x1017eb7bc`. The exact constructor and NUL-terminated input shape are
source-grounded; malformed pointer and outer exception behavior remain gaps.

Private Foundation captures used both literal `__NSCFConstantString` and
runtime strings built by the probe's `[[NSString alloc]
initWithUTF8String:]` path (which may materialize as
`NSTaggedPointerString` or `__NSCFString`) and found the same values for the
recorded domain. This runtime factory is probe provenance; it is not claimed to
be the official decoder's `stringWithCString:encoding:` implementation. The
fixture retains all 40 initial inputs and all 24 follow-up inputs, including
overflow, prefix, NUL, sign, control-character, selected-whitespace, Arabic,
Devanagari, and fullwidth-digit cases. These are captured examples rather than
a grammar claim.

The bounded vectors are in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-foundation-string-kvc.json`.
The test guards the exact captured input domain, then independently parses the
captured sign/whitespace/digit/prefix rules and saturates at the int32 bounds.
It rejects unsupported inputs rather than presenting a universal parser. Private
provenance is the authorized
Foundation probe receipt under `/var/folders/.../mooo-parent-string-kvc-*` and
the official decoder disassembly under `.lab/credential-storage/raw/cs1/`; no
account data or proprietary binary is tracked.
