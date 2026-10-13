# KICKOUT push notification projection

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02; wire text fields and logout alert traced 2026-10-11. Client build:
macOS KakaoTalk 26.8.0.

The manager's `locoManager:didReceiveKickOutPushNotice:` callback routes its
block through the shared main-thread helper: on the main thread it invokes the
block inline, and otherwise dispatches it to the main queue. Its observed block creates a mutable user
info dictionary, always inserts the notice reason under
`LPErrorLocoKickoutedReasonCodeKey`, and conditionally inserts non-null values
under `LPErrorErrMsg`, `LPErrorErrUrlKey`, and `LPErrorErrUrlLabelKey`. (An
earlier version of this note dropped the `Key` suffix from the last two.) The
check is for nil only, so an empty string is still inserted. It then creates
a typed error with numeric type `0x24` and posts the
`LPErrorLocoKickedOutNotification` notification.

The downstream `locoDidKickout:` consumer is a separate chain: its reviewed IMP
directly dispatches projection work to the main queue. The relationship between
that downstream consumer and the manager callback, including observer/session
effects, remains untraced.
The source does not establish whether this projection resets carriage/session
state, how observers consume the notification, or how the alternate
`CHANGESVR` notice is handled. Those remain explicit gaps.

## Static provenance

- `handleKickOutPushNotice:packetHeader:` IMP `0x101515b5c` forwards to
  `locoManager:didReceiveKickOutPushNotice:` when the manager responds.
- Manager callback block creator: `0x101413c30`; main-queue block:
  `0x10141e388`.
- Observed keys are `LPErrorLocoKickoutedReasonCodeKey`, `LPErrorErrMsg`,
  `LPErrorErrUrlKey`, and `LPErrorErrUrlLabelKey`; notification name is
  `LPErrorLocoKickedOutNotification`.
- The fixture is input-derived and validates optional-field insertion order,
  error type, and notification posting. Runtime observer effects are unexecuted.

## Wire text fields (2026-10-11)

The KICKOUT model's only own property is the int32 `reason`. Its push-notice
base declares three string properties, `errMsg`, `errUrl` and `errUrlLabel`.
No class in the chain overrides the key mapping or the validation fields, and
the JSON model initializer walks the base class's properties too. The body
therefore carries them as optional top-level keys with those exact names. The
callback reads each through its accessor to fill the three user-info entries
above.

The same three keys appear on the LOCO response base, where `errUrl` and
`errUrlLabel` are read directly from the response body. One decoder for the
optional `{errMsg, errUrl, errUrlLabel}` strings can therefore serve both
pushes and responses.

## Logout alert

The authentication controller's kicked-out handler reads the same four
user-info keys. It acts only while logged in and not already logging out. It
logs out first, resetting the database for reasons 1 and 10, then shows an
alert:

- The title is always the localized "logged out" title.
- **Reason 0:** the body is the localized "logged in on another device" text,
  even when the server sent `errMsg`.
- **Any other reason with `errMsg`:** the body is `errMsg`, shown verbatim.
- **Any other reason without `errMsg`:** the body is a localized generic list
  of logout causes: remote logout, login on another device, unregistering,
  account deletion or a password change.
- **Buttons:** the default button is OK. A second button labelled `errUrlLabel`
  appears only when both `errUrl` and `errUrlLabel` are present. Choosing it
  opens `errUrl` in the default browser. A URL without a label has no button.

The bridge reports the same choice in its `kakao-kicked-out` state message,
with its own wording for the two localized defaults. It appends
"label: URL" under the same both-present condition, and saves the text with
the kickout record so a restart reports the same message. One deviation: an
empty `errMsg` or URL counts as absent in the bridge, while the Mac alert would
show the empty string.

Gaps: the `addObserver` registration that connects this handler to the
notification was not found; the link rests on matching key constants and the
error object type. Also untraced: the alert's behavior with no second button,
how the model handles a non-string value in a text field, and any runtime
observation of a real KICKOUT body.
