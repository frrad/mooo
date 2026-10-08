# Observed album shape with generated images

The fixture preserves an owned Android 26.8.2 type-27 album's two-image ordering,
MIME types, dimensions, byte lengths, checksums and generated PNG bytes. It
replaces media keys, all URLs, identity fields and expiry with synthetic values.
No Kakao artwork or private conversation content is included.

The source PNGs are 64×64 RGBA swatches: R=4*x, G=4*y,
B=2*(x+y+30*i) modulo 256, A=255, with i=0/1. Encode with Go image/png.
The official picker was configured for Original quality and Collage Photos.
Tests feed the fixture to the production parser/converter/download paths; the
fixture does not establish maximum-size, alternate-resource or full-client parity.
