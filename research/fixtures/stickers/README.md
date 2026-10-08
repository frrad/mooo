# Synthetic sticker fixtures

`codec.json` contains executed official-client outputs for byte sequences
0..127 and 0..255. These are account-independent transform vectors.

The WebP files contain no Kakao artwork. Generate two 64×64 opaque RGB frames
with R=4*x, G=4*y, B=2*(x+y+30*i) modulo 256, for frame index i=0/1.
`synthetic.webp` was encoded from frame 0 with `cwebp -q 75`;
`synthetic-animated.webp` uses `img2webp -loop 0 -lossless -d 150` for each frame.
Tests derive wire bytes using the independent official codec vector, then call
the production resource decoder and compare original WebP bytes and dimensions.
