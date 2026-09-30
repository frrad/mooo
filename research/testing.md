# Client test strategy

The test suite separates wire primitives, client orchestration, and live
interoperability so routine CI never needs an account or network connection.

## Scripted backend

`internal/client/mock_backend_test.go` provides an in-memory scripted Kakao
backend using `net.Pipe`. Production keeps its normal TLS and LOCO-v3 dialers;
tests inject connection factories only at the dialing boundary. Client and
server sides still exchange real LOCO frames, BSON documents, correlated packet
IDs, and encrypted LOCO-v3 envelopes. The secure handshake itself remains
covered separately with synthetic vectors because a mock server must not depend
on Kakao's private key.

Each script consumes an exact ordered sequence and fails on an unexpected
method, BSON type/value, extra request, missing request, or timeout. Current
scenarios cover:

- `GETCONF` -> `CHECKIN` -> `LOGINLIST` -> paginated `LCHATLIST`;
- an unsolicited `MSG` delivered while the client is idle;
- exact baseline text `WRITE` shape and correlated response;
- generic one-member `CREATE` and direct-chat response decoding;
- `SHIP` -> media `POST` -> offset-resumed encrypted bytes -> `COMPLETE`;
- `LOGINLIST -950`, one HTTP token renewal, and one fresh login with the rotated
  credential;
- resumed `LOGINLIST` with persisted chat/max, token, and blind-token cursors;
- accepted `LOGINLIST -305` and partial `LCHATLIST -310`, proving useful chat and
  deletion deltas survive while pagination stops and global cursors do not advance;
- full-login inventory followed by an empty delta login across checkpoint reopen,
  proving the chat target survives without becoming an acknowledgement;
- official deletion-before-chat-data ordering, proving a same-page recreated chat
  regains its recovery target without inheriting the deleted commit boundary;
- duplicate suppression plus explicit message commits across checkpoint reopen;
- paged `SYNCMSG` catch-up, sparse ordered log IDs, and fail-closed no-progress
  handling that durably records an unresolved interval without prematurely
  advancing the checkpoint;
- version-2 checkpoint migration plus sorted gap persistence, conservative merge,
  prefix resolution, newer-tail preservation, and deleted-chat cleanup;
- disconnect after `WRITE`, proving no retry or implicit reconnect; and
- disconnect after uploaded photo bytes but before `COMPLETE`, proving no
  repeated `SHIP`, `POST`, or media connection.

All data is synthetic and account-independent. No endpoint, credential, media
URL, device identifier, or message from a live profile is used as a fixture.

## Other layers

- Protocol package tests pin framing, encryption, BSON/JSON widths, validation,
  hostile lengths, media checksums, URL policy, and registration state-machine
  transitions.
- HTTP components use injected round trippers or doers to verify exact requests,
  bounded responses, cancellation, and redaction.
- Private owned-account probes remain a manual interoperability gate for facts a
  mock cannot prove, such as acceptance by the current Kakao service and
  rendering by an official client. Their secrets and artifacts never enter Git.

CI runs race-enabled tests, vet, formatting, lint, vulnerability scanning, and
secret scanning for every pull request.
