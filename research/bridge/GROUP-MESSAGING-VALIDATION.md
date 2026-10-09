# Owned three-person group validation

On 2026-10-09, three disposable Android accounts running official KakaoTalk
26.8.2 (version code 29260820) participated in a regular group. The bridge used
its existing, exclusively leased secondary-device profile for one participant,
a local Synapse homeserver, and the pure-Go Olm backend. Only synthetic text and
a synthetic profile image were used. Private identifiers, credentials, database,
and captures remain outside the repository.

The newest account could add the other owned accounts by phone, but its first
group creation/send was rejected by Kakao user protection measures. That action
was not retried. An established participant created the group through the native
Regular Chat flow; all three official clients showed its first text. No Talk
Safety Pass, account recovery, backup, or restore was used.

## Messaging and metadata

A live message from the new participant created the Matrix portal. The tester
joined it and the appservice bot enabled Megolm. Subsequent texts from the two
remote participants appeared exactly once as encrypted events from distinct
ghost senders. The existing companion Matrix SDK device decrypted both to the
exact expected text. One SDK-encrypted Matrix text appeared on all three native
clients, including the bridge account's primary device.

The initial portal incorrectly used participant nicknames as its name, although
all native clients showed the chosen shared name. A single CHATINFO observation
returned a MultiChat with no personal `m` object and a `chatMetas` entry of type
3 whose plain string `content` exactly matched that native name. The connector
now consumes that shared name when a personal name is absent. A regression test
through GetChatInfo reproduced the original failure before the fix. Synthetic
cases cover revision selection, an empty latest value, and the existing personal
name override. Those edge-case policies are implementation decisions; the live
observation proves only the nonempty type-3 case.

An encrypted `sync-portal` command with the fixed bridge updated the existing
portal to the exact native group name. The complete roster contained all three
Kakao identities, the room was not classified as a DM, and the original
participant's synthetic profile avatar was present.

## Restart

After stopping the bridge, C sent one synthetic text. Restarting with the same
profile and database recovered it exactly once as an encrypted event. All
previous group message event IDs remained unchanged. The same SDK device and
retained crypto database decrypted the two earlier texts and the offline text.

## Scope and gaps

This is observed evidence for regular three-person text messaging with encrypted
Matrix transport. Kakao regular chats themselves are not end-to-end encrypted;
this test does not claim Kakao Secret Chat support. It does not validate Beeper
hosting, Open Chat, Team Chat, changes in membership, group avatars, or every
message type. The full official-client shared-name consumer/persistence chain
and rename notification behavior remain untraced. New accounts may face service
protection restrictions even when ordinary receipt and messaging work.
