# Nudge type availability audit

Research date: 2026-10-09. Authorized Android KakaoTalk 26.8.2 APK;
JADX 1.5.6 scoped class inspection. No public protocol implementation consulted.
These are static observations, not an executed or observed message fixture.

The recovered enum names `Nudge=21`. In `ChatLogViewType.kt`, the synthetic
switch mapping (`pc9$c4$a`, classes10.dex) assigns Nudge branch 54. The consuming
view selector (`pc9$c4.b`, classes8.dex) groups that branch with unsupported
message types and selects the outgoing or incoming unsupported view according
to message ownership. This documents the ordinary selector path; earlier special
cases in that selector have not been proven irrelevant for every possible
Nudge envelope.

Source fingerprints:

- classes8.dex SHA-256: `a909489a820c077c558dea67a3e67ec7d4da5215ed16f9fe5328d77f3785e53f`.
- classes10.dex SHA-256: `792d9793080799ac964af1992a11206b6a092f35b552bfa3f8d4254b2a8390b8`.

An enum-reference search found the mapping but no direct Nudge sender reference.
That search is a lead: generic send paths can receive a type dynamically, so it
does not prove that emission is impossible. Other classes with “Nudge” in their
names concern recommendations or payment UI and are not evidence of type-21
messages.

No owned-account type-21 message has been captured. Request and response models,
sender callers, callbacks, persistence, special-case consumers, and failure
behavior remain untraced. mooo continues to retain the message identity and
report an unsupported-message notice; no inferred Nudge payload decoder is added.
