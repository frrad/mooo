# SL-EXP-004 — Live persistent session and inbound message

Date: 2026-09-28

## Scope and controls

This bounded experiment used the maintainer-owned disposable Android primary and
the clean-room Go client identity. Live credentials, device/account identifiers,
route addresses, QR data, verification codes, cursor values, and message text were
kept outside the repository. The test message was synthetic and was sent only to
the same account's self-chat.

## Method

1. Approve one fresh client-generated QR on Android and complete the four-character
   device-authorization step.
2. Atomically persist the returned authentication fields in the client-owned state.
3. Start a new Go process from that state and run `GETCONF`, `CHECKIN`, secure-v3
   carriage setup, and `LOGINLIST`.
4. Disconnect and repeat from another fresh process without another QR approval.
5. Keep one authenticated carriage connected, send a known synthetic sentence from
   Android to the account's self-chat, and recursively decode incoming BSON strings.

## Observations

- The current unregistered-device QR response used status `-100` with `passcode`,
  `remainingSeconds`, and `nextRequestIntervalInSeconds`; approval then returned a
  complete status-0 credential response.
- Booking and check-in each returned status 0 over TLS. The assigned carriage used
  the reviewed secure-v3 RSA-OAEP/AES-GCM transport, and `LOGINLIST` returned status
  0.
- Repeating the complete bootstrap in a fresh process used the persisted state and
  required no new approval.
- The connected client received the synthetic text as an unsolicited `MSG`. Its
  top-level BSON keys were `chatId`, `chatLog`, `logId`, `noSeen`, `pushAlert`, and
  `status`, and recursive typed decoding matched the exact known sentence.

## Result

The reversed-client vertical slice now demonstrates persistent secondary-device
authentication, repeatable authenticated LOCO sessions, and live inbound text
delivery. Production connection management, complete `chatLog` typing, message
acknowledgement, cursor persistence, and gap recovery remain implementation work.
