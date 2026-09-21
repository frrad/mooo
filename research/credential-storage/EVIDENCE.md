# Credential-storage evidence ledger

Append entries as evidence is collected. Do not rewrite hypotheses into facts;
supersede them with a new entry and link the earlier ID.

| ID | Date | Version | Evidence class | Statement | Confidence | Status |
| --- | --- | --- | --- | --- | --- | --- |
| CS-PUB-001 | 2026-09-20 | macOS 26.4.1 | Public prior art | At pinned revision `87743a438fa2eebf101f6df701790e9721fd6619`, OpenKakao reports candidate preference values decoding to 96 bytes, several fixed string/key references, and a possible platform-identifier/PBKDF2/CommonCrypto path. | Medium | Reproduce mappings and recipe independently |
| CS-BIN-001 | 2026-09-20 | macOS 26.8.0 | Observed/static | The current binary retains the fixed preference strings, secure preference-access surface, platform-identifier lookup, parameterized PBKDF2 helper, and CommonCrypto KDF/cipher imports. | High | Confirmed presence; relationships unresolved |
| CS-BIN-002 | 2026-09-20 | macOS 26.8.0 | Observed/static | Generic AES key/IV helper selectors cited by prior work are owned by backup and session-transport components in current runtime metadata; their connection to preference encryption is not established. | High | Prevents anchor-by-name shortcut |
| CS-HYP-001 | 2026-09-20 | macOS 26.8.0 | Hypothesis | The selected preference values use a key derived from the platform identifier through PBKDF2 and are encrypted with CommonCrypto. | Medium | Test in CS-1 through CS-3 |
| CS-HYP-002 | 2026-09-20 | macOS 26.8.0 | Hypothesis | The 96-byte decoded value contains an IV or nonce, ciphertext, and possibly an authentication tag. | Low | Do not implement until proven |

## Transfer reviews

No binary-analysis finding from this investigation has yet passed a transfer review
into implementation.
