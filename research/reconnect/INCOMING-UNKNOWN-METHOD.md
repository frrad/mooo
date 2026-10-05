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
`NSStringFromClass`, `substringFromIndex:`, `stringWithFormat:`, and
`NSSelectorFromString`. It checks the weak owner with
`respondsToSelector:` before calling `performSelector:withObject:withObject:`
with the constructed notice and original packet header. An absent class
therefore reaches the selector derivation/gate with no usable notice and has no
registered delegate or receipt call in the bounded source model. The exact
format string, substring index, and Foundation behavior for a nil selector are
not published here; callback exception and disconnect policy are also gaps.

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
