# Credential-storage evidence ledger

Append entries as evidence is collected. Do not rewrite hypotheses into facts;
supersede them with a new entry and link the earlier ID.

| ID | Date | Version | Evidence class | Statement | Confidence | Status |
| --- | --- | --- | --- | --- | --- | --- |
| CS-PUB-001 | 2026-09-20 | macOS 26.4.1 | Public prior art | At pinned revision `87743a438fa2eebf101f6df701790e9721fd6619`, OpenKakao reports candidate preference values decoding to 96 bytes, several fixed string/key references, and a possible platform-identifier/PBKDF2/CommonCrypto path. | Medium | Reproduce mappings and recipe independently |
| CS-BIN-001 | 2026-09-20 | macOS 26.8.0 | Observed/static | The current binary retains the fixed preference strings, secure preference-access surface, platform-identifier lookup, parameterized PBKDF2 helper, and CommonCrypto KDF/cipher imports. | High | Confirmed presence; relationships unresolved |
| CS-BIN-002 | 2026-09-20 | macOS 26.8.0 | Observed/static | Generic AES key/IV helper selectors cited by prior work are owned by backup and session-transport components in current runtime metadata; their connection to preference encryption is not established. | High | Prevents anchor-by-name shortcut |
| CS-BIN-003 | 2026-09-20 | macOS 26.8.0 | Observed/static | The secure preference writer passes input bytes and a separate caller-supplied input through a dedicated encryption helper before using the ordinary record-update path; the ordinary writer stores its input unchanged. | High | Confirmed by complete wrapper-to-record data flow |
| CS-BIN-004 | 2026-09-20 | macOS 26.8.0 | Observed/static | The matching secure reader retrieves stored bytes, passes them and the separate caller-supplied input through the inverse helper, and returns that result directly. Missing storage, record, value, or successful transform all collapse to either a value or no value at this layer. | High | Confirmed by complete record-to-wrapper data flow |
| CS-BIN-005 | 2026-09-20 | macOS 26.8.0 | Observed/static | One candidate defaults key has an active inverse pair: read string → base64 decode → decrypt with the local device UUID; write bytes → encrypt with the device UUID → base64 encode → store. Automatic login additionally converts the decrypted bytes to hexadecimal text before use. | High | CS-1 routing gate passed; trace helper internals next |
| CS-BIN-006 | 2026-09-20 | macOS 26.8.0 | Observed/static | The second historical candidate appears only in cleanup/reset behavior. No current producer, cryptographic call, or shared recipe was found for it. | High | Excluded from CS-2/CS-3 absent new producer evidence |
| CS-BIN-007 | 2026-09-20 | macOS 26.8.0 | Observed/static | The active authentication-value path calls the low-level encryption helpers directly rather than using the generic secure preference wrapper. | High | Narrows CS-2/CS-3 entry points |
| CS-PUB-002 | 2026-09-20 | OpenKakao history through `b03ef74` | Public prior art | OpenKakao does not contain recovered parameters or a working decryptor for these preference values. Its implemented local-database recipe concerns an older SQLCipher database path, and later issue work corrected unrelated backup/PIN KDF values that had initially been associated with local storage. | High | Treat the preference note as leads, not a recipe |
| CS-HYP-001 | 2026-09-20 | macOS 26.8.0 | Hypothesis | The active candidate value uses a key derived from the device UUID through PBKDF2 and is encrypted with CommonCrypto. | Medium | Candidate routing confirmed; KDF/cipher relationship remains for CS-2/CS-3 |
| CS-HYP-002 | 2026-09-20 | macOS 26.8.0 | Hypothesis | The 96-byte decoded value contains an IV or nonce, ciphertext, and possibly an authentication tag. | Low | Do not implement until proven |

## Transfer reviews

No binary-analysis finding from this investigation has yet passed a transfer review
into implementation.
