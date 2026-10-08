package media

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestOfficialStickerPrefixVectors(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/stickers/codec.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			InputHex    string `json:"input_hex"`
			ExpectedHex string `json:"expected_hex"`
		}
	}
	if json.Unmarshal(data, &fixture) != nil || len(fixture.Cases) != 2 {
		t.Fatal("fixture invalid")
	}
	for _, c := range fixture.Cases {
		input, _ := hex.DecodeString(c.InputHex)
		want, _ := hex.DecodeString(c.ExpectedHex)
		out, err := decodeStickerPrefix(input, true)
		if err != nil || !bytes.Equal(out, want) {
			t.Fatal("official decoder vector mismatch")
		}
		if !bytes.Equal(input[128:], out[128:]) {
			t.Fatal("resource tail changed")
		}
	}
	if _, err := decodeStickerPrefix(make([]byte, 127), true); !errors.Is(err, ErrInvalidStickerResource) {
		t.Fatal("short encoded file admitted")
	}
}

func TestStickerAttachmentAndFixedOrigin(t *testing.T) {
	for _, s := range []string{`{"path":"../private.png"}`, `{"path":"https://example.invalid/a.png"}`, `{"path":"a.webp","path":"b.webp"}`, `{"path":"/absolute.webp"}`, `{"path":"a.webp?token=synthetic"}`, `{"path":"a.webp"} {}`, `{"path":null}`} {
		if _, err := DecodeStickerAttachment(s); err == nil {
			t.Fatalf("invalid attachment accepted: %s", s)
		}
	}
	a, err := DecodeStickerAttachment(`{"path":"synthetic/001.png","name":"synthetic","unknown":{"future":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := StickerURL(a)
	if err != nil || uri != "https://item.kakaocdn.net/dw/synthetic/001.png" {
		t.Fatal("wrong origin/path")
	}
}

type stickerTransport func(*http.Request) (*http.Response, error)

func (f stickerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStickerDownloadExactPNGAndFailures(t *testing.T) {
	var buf bytes.Buffer
	if png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 3))) != nil {
		t.Fatal("PNG")
	}
	data := buf.Bytes()
	for _, status := range []int{200, 404, 410, 500, 302} {
		requests := 0
		client := &http.Client{Transport: stickerTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.URL.Host != "item.kakaocdn.net" || r.Header.Get("Authorization") != "" {
				t.Fatal("wrong host or credentials")
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{"Location": []string{"https://example.invalid/private"}}, Request: r}, nil
		})}
		out, err := DownloadSticker(context.Background(), client, StickerAttachment{Path: "synthetic.png"})
		if status == 200 {
			if err != nil || out.Width != 2 || out.Height != 3 || !bytes.Equal(out.Data, data) {
				t.Fatal("PNG bytes/dimensions changed")
			}
		} else if err == nil {
			t.Fatal("HTTP failure accepted")
		}
		if status == 404 || status == 410 {
			if !errors.Is(err, ErrStickerUnavailable) {
				t.Fatal("unavailable error lost")
			}
		}
		if requests != 1 {
			t.Fatal("redirect followed")
		}
	}
	if _, err := DecodeStickerResource("a.png", make([]byte, MaxStickerBytes+1)); !errors.Is(err, ErrInvalidStickerResource) {
		t.Fatal("oversized resource admitted")
	}
}

// Use the official executed vector's keystream, independently of mooo's
// implementation, to prepare wire bytes for synthetic media parser tests.
func officialEncodedResource(t *testing.T, plain []byte) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/stickers/codec.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			InputHex    string `json:"input_hex"`
			ExpectedHex string `json:"expected_hex"`
		}
	}
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("fixture")
	}
	input, _ := hex.DecodeString(f.Cases[0].InputHex)
	output, _ := hex.DecodeString(f.Cases[0].ExpectedHex)
	if len(plain) < 128 {
		t.Fatal("encoded fixture too small")
	}
	wire := bytes.Clone(plain)
	for i := 0; i < 128; i++ {
		wire[i] ^= input[i] ^ output[i]
	}
	return wire
}

func TestStaticAndAnimatedWebPBytesPreserved(t *testing.T) {
	for _, name := range []string{"synthetic.webp", "synthetic-animated.webp"} {
		plain, err := os.ReadFile("../../../research/fixtures/stickers/" + name)
		if err != nil {
			t.Fatal(err)
		}
		resource, err := DecodeStickerResource("synthetic.webp", officialEncodedResource(t, plain))
		if err != nil || resource.Width != 64 || resource.Height != 64 || resource.MIME != "image/webp" || !bytes.Equal(resource.Data, plain) {
			t.Fatalf("WebP preservation failed: %s %v", name, err)
		}
		bad := bytes.Clone(plain)
		bad[4] = 0
		if _, err := DecodeStickerResource("synthetic.webp", officialEncodedResource(t, bad)); !errors.Is(err, ErrInvalidStickerResource) {
			t.Fatal("bad RIFF length admitted")
		}
	}
}

func TestAnimatedGIFPreserved(t *testing.T) {
	palette := make(color.Palette, 256)
	for i := range palette {
		palette[i] = color.RGBA{uint8(i), uint8(255 - i), uint8(i / 2), 255}
	}
	frames := []*image.Paletted{}
	for f := 0; f < 2; f++ {
		img := image.NewPaletted(image.Rect(0, 0, 64, 64), palette)
		for i := range img.Pix {
			img.Pix[i] = byte(i*7 + i/64 + f)
		}
		frames = append(frames, img)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	resource, err := DecodeStickerResource("synthetic.gif", officialEncodedResource(t, buf.Bytes()))
	if err != nil || resource.MIME != "image/gif" || resource.Width != 64 || !bytes.Equal(resource.Data, buf.Bytes()) {
		t.Fatal("animated GIF changed", err)
	}
}
