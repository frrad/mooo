# Invalid UTF-8 and dictionary insertion boundary

Status: platform-bounded synthetic contract. Observation date: 2026-10-04.
The account-free Foundation probe ran on macOS 26.6.2 (25G83), arm64.

The traced type-2 string path constructs a string from a NUL-terminated C
pointer. Malformed or truncated UTF-8 makes the Foundation string factory return
nil. The source helper's nil check (`0x1017eb7c4`/`0x1017eb930`) skips assignment and
continues to later fields. A later nonnil value for the same key replaces an
earlier value; an invalid value therefore preserves any earlier value.

Dictionary insertion is a separate boundary. A nonnil value with an invalid
(nil) key reaches the source insertion path (`0x1018eb4bc`/`0x1018eb4cc`) and is an explicit
failure; the nil-key factory is at `0x1018eb53c`. A nil value is skipped before that
insertion call, so an invalid key paired with an invalid value does not fail.
The fixture uses malformed UTF-8 bytes in canonical, bounded BSON string
vectors and preserves decoded strings. It does not claim a general BSON
parser or Unicode grammar and does not copy proprietary source.

The fixture is
[`research/fixtures/reconnect/rc-q5-invalid-utf8-dictionary.json`](../fixtures/reconnect/rc-q5-invalid-utf8-dictionary.json)
with `static` provenance: the Foundation behaviour was probed directly, but the
official client has not run these inputs. The sanitized probe output is
retained outside the repository.

## mooo today

- `loco.DecodeObservedBSON` reproduces all ten cases
  (`internal/protocol/loco/invalid_utf8_dictionary_test.go`).
- The production receive path decodes with mongo-driver, which differs on six
  of the ten cases. `bsonshadow.Compare`, which the client runs on received
  bodies, reports each of them
  (`internal/protocol/bsonshadow/invalid_utf8_dictionary_test.go`):

| Case | Official client | mongo-driver | Shadow kind |
| --- | --- | --- | --- |
| `invalid_value_then_later` | drops the invalid value | keeps the raw bytes | `official-dropped-invalid-utf8` |
| `duplicate_valid_then_invalid` | keeps the earlier valid value | overwrites with the invalid bytes | `value-differs` |
| `invalid_key_invalid_value` | drops the pair | keeps both | `official-dropped-invalid-utf8` |
| `invalid_key_valid_value` | rejects the document | accepts it | `official-rejects` |
| `invalid_key_empty_value` | rejects the document | accepts it | `official-rejects` |
| `first_nul_truncates_invalid_tail` | ends the string at the NUL | keeps the declared length | `string-nul-truncated` |

Whether the receive path should adopt the official behaviour for these cases,
or record a deliberate deviation, is an open decision tracked in `PLAN.md`.
