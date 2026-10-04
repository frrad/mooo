# KICKOUT push notification projection

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

The manager's `locoManager:didReceiveKickOutPushNotice:` callback unconditionally
dispatches a block onto the main queue. Its observed block creates a mutable user
info dictionary, always inserts the notice reason under
`LPErrorLocoKickoutedReasonCodeKey`, and conditionally inserts non-null values
under `LPErrorErrMsg`, `LPErrorErrUrl`, and `LPErrorErrUrlLabel`. It then creates
a typed error with numeric type `0x24` and posts the
`LPErrorLocoKickedOutNotification` notification.

The direct main-queue dispatch is distinct from the CHANGESVR callback's shared
main-thread helper; KICKOUT does not invoke that helper in the reviewed IMP.
The source does not establish whether this projection resets carriage/session
state, how observers consume the notification, or how the alternate
`CHANGESVR` notice is handled. Those remain explicit gaps.

## Static provenance

- `handleKickOutPushNotice:packetHeader:` IMP `0x101515b5c` forwards to
  `locoManager:didReceiveKickOutPushNotice:` when the manager responds.
- Manager callback block creator: `0x101413c30`; main-queue block:
  `0x10141e388`.
- Observed keys are `LPErrorLocoKickoutedReasonCodeKey`, `LPErrorErrMsg`,
  `LPErrorErrUrl`, and `LPErrorErrUrlLabel`; notification name is
  `LPErrorLocoKickedOutNotification`.
- The fixture is input-derived and validates optional-field insertion order,
  error type, and notification posting. Runtime observer effects are unexecuted.
