# Invalid UTF-8 and dictionary insertion boundary

Status: platform-bounded synthetic contract. Observation date: 2026-10-04.
The account-free Foundation probe ran on macOS 26.6.2 (25G83), arm64.

The traced type-2 string path constructs a string from a NUL-terminated C
pointer. Malformed or truncated UTF-8 makes the Foundation string factory return
nil. The source helper checks that result and skips assignment, then continues
to later fields. A later nonnil value for the same key replaces an earlier
value; an invalid value therefore preserves any earlier value.

Dictionary insertion is a separate boundary. A nonnil value with an invalid
(nil) key is an explicit insertion failure. A nil value is skipped before that
insertion call, so an invalid key paired with an invalid value does not fail.
The fixture uses malformed UTF-8 bytes in otherwise bounded BSON string
vectors and models only those observed values (`7` and `42`). It does not claim
a general BSON parser or Unicode grammar and does not copy proprietary source.

The executable contract is in
`internal/protocol/sessionlogin/invalid_utf8_dictionary_contract_test.go`; the
sanitized Foundation result is retained outside the repository at
`/private/tmp/mooo-invalid-utf8-source-contract-20261004.md` and
`/private/tmp/mooo-dictionary-nil-20261004.txt`.
