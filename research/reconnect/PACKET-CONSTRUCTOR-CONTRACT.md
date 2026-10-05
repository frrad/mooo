# Receipt-to-packet constructor contract

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

The receipt-side producer at `0x101355cac` reads `packetId`, `method`, and
`JSONObject`, then calls `initWithPacketId:method:body:`. The LocoPacket
initializer IMP is `0x10175a0f8`, with types `@36@0:8I16@20@28`: packet ID is
uint32, method and body are objects. It first calls the superclass initializer
and stops at a nil result. For a nonnil result it stores the body object, then
constructs and stores the embedded header.

The embedded header initializer is `initWithPacketId:statusCode:method:bodyType:bodyLength:`. `0x1018dc560` is the Objective-C message stub; selector metadata resolves its target IMP to `0x10185aa50`. The observed inputs carry the original uint32 ID and method; status code is an unsigned 16-bit field, while body type and body length are initialized to zero. After successful packet super-init, a nil header result is still passed to `setHeader:`; only a nil packet super-init suppresses body/header stores. The fixture keeps body identity opaque and asserts both nil boundaries and store order. It does not infer BSON or encryption from this constructor.

The next layer is separate: the LocoPacket body getter is `0x10175a3ac`, then
`packetData` obtains that body and calls `BSONData`. Packet framing, encryption,
socket admission, and lower write-tag derivation are separate contracts.

## Provenance

- Private receipt producer report: `0x101355cac` reads packetId, method,
  JSONObject, then calls `initWithPacketId:method:body:`.
- Private wire-chain receipts: `receipt-init.txt` and `body-getter.txt`.
- Private constructor metadata: `initWithPacketId:statusCode:method:bodyType:bodyLength:`
  at `0x1018dc560`.
