# Unknown incoming notice methods

Status: reviewed static source and synthetic model; runtime delivery and outer
exception behavior remain unexecuted. Client build: macOS KakaoTalk 26.8.0.

The default receive handler block at `0x10152049c` obtains
`packet.header.method`, looks up a notice class in the receive-handler map with
`objectForKeyedSubscript:`, then calls `alloc` and `initWithPacket:` on the
lookup result. There is no explicit class-lookup nil branch in this block.
For an absent method mapping, Objective-C nil messaging makes allocation and
initializer calls return nil; this is a Foundation behavior represented by the
synthetic fixture, not a Go runtime policy.

The block then derives the manager callback selector from the selected class:
`NSStringFromClass`, `substringFromIndex:4`, `stringWithFormat:` using the
`handle%@:packetHeader:` format, and `NSSelectorFromString`. It checks the weak owner with
`respondsToSelector:` before calling `performSelector:withObject:withObject:`
with the constructed notice and original packet header. An absent class
therefore reaches the selector derivation/gate with no usable notice. The
source proves only the conditional owner gate: a false gate skips
`performSelector`, while a true gate would receive the nil notice and original
header. It does not prove an unconditional unknown-method drop or the later
delegate/receipt behavior. Foundation behavior for a nil class name and the
outer callback exception/disconnect policy remain gaps.

This path is distinct from a recognized HINT initializer that returns nil for a
nonnull body: the default block does not check the initializer result before
performing its owner selector gate. Whether the selected manager callback and
receipt constructor accept that nil notice is modeled separately in the typed
notice contract and is not generalized to outer exception handling.

The synthetic fixture is
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-incoming-unknown-method.json`.
It records only the absent lookup, nil-safe constructor path, selector-gate
boundary, and lack of downstream calls; it does not activate a runtime notice
dispatcher or receipt transport.
