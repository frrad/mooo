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

Private Foundation captures used both literal and runtime-created NSString
classes and found the same values for the recorded domain: decimal `42`, tab,
space, NBSP, U+2003, and U+3000 prefixes were accepted; leading LF, CR, FF, and
vertical tab produced zero; Arabic-Indic and fullwidth decimal digits produced
`42`; `- 42` produced `-42`; a numeric prefix followed by letters produced the
numeric prefix; and signed overflow outside int64 saturated to the int32
bounds. These are captured examples rather than a grammar claim.

The bounded vectors are in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-foundation-string-kvc.json`.
The test uses exact captured input strings and rejects unsupported inputs rather
than embedding a general-purpose parser. Private provenance is the authorized
Foundation probe receipt under `/var/folders/.../mooo-parent-string-kvc-*` and
the official decoder disassembly under `.lab/credential-storage/raw/cs1/`; no
account data or proprietary binary is tracked.
