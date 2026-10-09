# Kakao contact-card inbound observations

2026-10-09, authorized owned Android KakaoTalk 26.8.2 A/B experiment.
No public prior art was consulted. This records observed behavior; type 4 is
implemented with scoped encrypted catch-up/live acceptance.

## Controlled share

A fictional contact, `Mooo-Synthetic-Contact` with reserved test number
`+1-202-555-0123`, was created in Android Contacts with **Device only** storage.
Kakao's Add Automatically setting was verified off before granting READ_CONTACTS
on the owned disposable emulator. Contacts → Send Contact opened the Android
Contacts picker. Selecting the one synthetic contact opened Kakao's Preview,
showing the name, Phone Number, Cell, and formatted number. SEND returned to the
previously verified owned B chat. The secure Android picker produced a black
screenshot, but its UI hierarchy exposed the synthetic name and selection row.
The Kakao preview did not expose its title or number through the UI hierarchy;
visual inspection confirmed them. Each submission boundary had a durable private
attempt receipt.

The initial send failed visibly. Private official-client logs identified an
UnknownHostException for `katalk.kakao.com`. Android network validation remained
true, while bounded emulator DNS lookups timed out for both Kakao and an unrelated
host. Host lookups succeeded. A Wi-Fi cycle failed to recover DNS. Cold booting
the same owned AVD with an explicitly configured, host-tested resolver restored
name resolution and preserved login. The existing failed card was explicitly
retried once through the official Re-send confirmation, rather than submitting
a second contact. A scoped receiver MSG capture then observed type 4, and the
official B chat displayed the synthetic contact card.

## Observed payload and resource

The attachment object contained strings `k`, `name`, `url`, `rsc`, `hfac`, and
`rt`, and integer `expire`. No `s` or `cs` field occurred. `expire` was a
13-digit millisecond timestamp. The observed name matched the selected contact.
Token/resource values stay private; their semantics are not established by their
names. The resource URL used HTTPS on `talk.kakaocdn.net`.

An independent bounded download returned Content-Type `etc/vcard` and 115 bytes.
The content was vCard 3.0 with LF lines:

```text
BEGIN:VCARD
VERSION:3.0
FN:Mooo-Synthetic-Contact
N:Mooo-Synthetic-Contact
TEL;TYPE=CELL:+1-202-555-0123
END:VCARD
```

These values were deliberately synthetic at the source. No actual account phone
number or personal contact was selected. A bridge should preserve the complete
vCard resource, including fields beyond this minimal example; a name-only text
rendering would lose the shared contact data. Matrix upload MIME/filename policy
must be stated separately from the unusual observed server Content-Type.

## Static leads and remaining work

Authorized single-class Android inspection maps `ContactChatLog.kt` to
`com.kakao.talk.db.model.chatlog.g` in classes10.dex. Its preview combines the
localized contact label and attachment `name`; JSON/resource failures fall back
to the base preview. This does not establish the upload or download contract.

Still trace the share request/response, upload callbacks, persistence, resource
consumer and failure behavior. Those untraced official layers remain explicit
gaps; the implementation supports the observed direct-resource contract.
Encrypted SDK decryption verified the original vCard SHA-256, filename, MIME,
size and readable body for catch-up and a fresh shared-script live delivery.
Production tests cover malformed/expired resources and upload-failure cursor
retention. After a normal restart, two independent contact identities remained
exactly two messages. The official B Details action eventually opened Android's
Add to contacts editor with the expected synthetic name and phone number; an
immediate snapshot had still shown the chat during resource loading. The editor
was dismissed without saving. Contact import, phone calls, SMS and automatic
address-book syncing are not part of this test.

## Scoped bridge implementation

The decoder validates a bounded unique-key JSON object, positive message identity,
nonempty bounded name/token, allowed HTTPS resource URL and millisecond expiry.
The direct resource is bounded to 1 MiB by local policy; no wire size or checksum
is invented. Framing requires one vCard 3.0, accepting LF and CRLF without changing
the uploaded bytes. Other vCard versions/resource-only forms remain gaps.
Matrix receives `m.file`, filename `contact.vcf`, MIME `text/vcard`, the actual
byte size and a readable contact-name body. The complete original resource is
uploaded through the library's encrypted-media path. The original `etc/vcard`
HTTP Content-Type is documented but not used as Matrix MIME.

## Reproducible synthetic sender

`research/emu.sh send-contact a` reads a private JSON request containing `peer`,
`name`, and an absolute owner-only `receipt` path. It requires the usual
`ALLOW_TEST_MESSAGES=1` authorization, an already selected owned chat, an empty
composer, and a precreated device-only name starting with `Mooo-Synthetic-`.
The operator must verify that fixture's fields before use and keep Add
Automatically off. The helper checks the Android Contacts picker package, exact
unique name and Kakao preview labels, reserves the receipt before SEND, and
confirms return to the owned chat. Unknown permission screens or preview names
stop before sending. It does not automate permissions, contact creation or
failed-message retries. A receipt blocks subsequent runs even if the outcome is
ambiguous. A fresh run passed owned A/B live encrypted delivery on 2026-10-09.
