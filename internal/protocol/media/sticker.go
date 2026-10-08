package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	"io"
	"net/http"
	"path"
	"strings"
)

const MaxStickerBytes = 4 << 20

// Public binary interoperability constant, not an account credential (research/stickers.md).
const stickerResourceKey = "a271730728cbe141e47fd9d677e9006d" //gitleaks:allow

var (
	ErrInvalidSticker         = errors.New("media: invalid sticker attachment")
	ErrUnsupportedSticker     = errors.New("media: unsupported sticker resource")
	ErrInvalidStickerResource = errors.New("media: invalid sticker resource")
	ErrStickerUnavailable     = errors.New("media: sticker resource unavailable")
	ErrStickerTransfer        = errors.New("media: sticker transfer failed")
)

type StickerAttachment struct{ Path, Sound string }

func (StickerAttachment) String() string     { return "StickerAttachment{<redacted>}" }
func (a StickerAttachment) GoString() string { return a.String() }

type StickerResource struct {
	Data            []byte
	MIME, Extension string
	Width, Height   int
}

// DecodeStickerAttachment retains only fields consumed by the inbound path.
func DecodeStickerAttachment(value string) (StickerAttachment, error) {
	if len(value) == 0 || len(value) > 16<<10 {
		return StickerAttachment{}, ErrInvalidSticker
	}
	dec := json.NewDecoder(strings.NewReader(value))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return StickerAttachment{}, ErrInvalidSticker
	}
	seen := map[string]bool{}
	var a StickerAttachment
	for dec.More() {
		tok, err = dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return a, ErrInvalidSticker
		}
		seen[key] = true
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return a, ErrInvalidSticker
		}
		switch key {
		case "path":
			err = json.Unmarshal(raw, &a.Path)
		case "sound":
			err = json.Unmarshal(raw, &a.Sound)
		}
		if err != nil {
			return a, ErrInvalidSticker
		}
	}
	if _, err = dec.Token(); err != nil {
		return a, ErrInvalidSticker
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !validStickerPath(a.Path) || len(a.Sound) > 512 {
		return a, ErrInvalidSticker
	}
	return a, nil
}

func validStickerPath(value string) bool {
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || path.Clean(value) != value {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/_.-", c) {
			continue
		}
		return false
	}
	return true
}

// StickerURL is a fixed-origin full-size resource request, never an arbitrary URL.
func StickerURL(a StickerAttachment) (string, error) {
	if !validStickerPath(a.Path) {
		return "", ErrInvalidSticker
	}
	return "https://item.kakaocdn.net/dw/" + a.Path, nil
}

func DownloadSticker(ctx context.Context, client *http.Client, a StickerAttachment) (StickerResource, error) {
	if a.Sound != "" {
		return StickerResource{}, ErrUnsupportedSticker
	}
	uri, err := StickerURL(a)
	if err != nil {
		return StickerResource{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return StickerResource{}, ErrStickerTransfer
	}
	// Copy the caller's transport/timeouts, but never follow a CDN redirect.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrUnsafeURL }
	resp, err := bounded.Do(req)
	if err != nil {
		if errors.Is(err, ErrUnsafeURL) {
			return StickerResource{}, ErrInvalidStickerResource
		}
		return StickerResource{}, ErrStickerTransfer
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return StickerResource{}, ErrStickerUnavailable
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return StickerResource{}, ErrInvalidStickerResource
	}
	if resp.StatusCode != 200 {
		return StickerResource{}, ErrStickerTransfer
	}
	if resp.ContentLength > MaxStickerBytes {
		return StickerResource{}, ErrInvalidStickerResource
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxStickerBytes+1))
	if err != nil {
		return StickerResource{}, ErrStickerTransfer
	}
	return DecodeStickerResource(a.Path, data)
}

// DecodeStickerResource preserves original animation bytes after the official
// resource transform. It doesn't transcode assets or interpret sticker artwork.
func DecodeStickerResource(resourcePath string, data []byte) (StickerResource, error) {
	ext := strings.ToLower(path.Ext(resourcePath))
	if ext != ".png" && ext != ".gif" && ext != ".webp" {
		return StickerResource{}, ErrUnsupportedSticker
	}
	if len(data) == 0 || len(data) > MaxStickerBytes {
		return StickerResource{}, ErrInvalidStickerResource
	}
	var err error
	data, err = decodeStickerPrefix(data, ext == ".gif" || ext == ".webp")
	if err != nil {
		return StickerResource{}, err
	}
	var w, h int
	var mime string
	if ext == ".webp" {
		var err error
		w, h, err = stickerWebPSize(data)
		if err != nil {
			return StickerResource{}, err
		}
		mime = "image/webp"
	} else {
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || "."+format != ext {
			return StickerResource{}, ErrInvalidStickerResource
		}
		w, h = config.Width, config.Height
		mime = "image/" + format
	}
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 || w*h > 16<<20 {
		return StickerResource{}, ErrInvalidStickerResource
	}
	return StickerResource{Data: data, MIME: mime, Extension: ext, Width: w, Height: h}, nil
}

// WebP uses a RIFF canvas header, including for animated resources. Validate
// bounded chunk structure and dimensions without allocating decoded frames.
func stickerWebPSize(data []byte) (int, int, error) {
	bad := ErrInvalidStickerResource
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return 0, 0, bad
	}
	w, h := 0, 0
	animated, anim, frame, imageData := false, false, false, false
	for at := 12; at < len(data); {
		if len(data)-at < 8 {
			return 0, 0, bad
		}
		n := int(binary.LittleEndian.Uint32(data[at+4 : at+8]))
		end := at + 8 + n
		if end < at || end > len(data) {
			return 0, 0, bad
		}
		chunk := data[at+8 : end]
		switch string(data[at : at+4]) {
		case "VP8X":
			if at != 12 || n != 10 {
				return 0, 0, bad
			}
			w, h = uint24(chunk[4:7])+1, uint24(chunk[7:10])+1
			animated = chunk[0]&2 != 0
		case "VP8 ":
			if n < 10 || !bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
				return 0, 0, bad
			}
			imageData = true
			if w == 0 {
				w = int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
				h = int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			}
		case "VP8L":
			if n < 5 || chunk[0] != 0x2f {
				return 0, 0, bad
			}
			imageData = true
			if w == 0 {
				v := binary.LittleEndian.Uint32(chunk[1:5])
				w = int(v&0x3fff) + 1
				h = int((v>>14)&0x3fff) + 1
			}
		case "ANIM":
			if !animated || anim || n != 6 {
				return 0, 0, bad
			}
			anim = true
		case "ANMF":
			if !animated || !anim || n < 24 || !validStickerWebPFrame(chunk[16:]) {
				return 0, 0, bad
			}
			frame = true
			if uint24(chunk[0:3])*2+uint24(chunk[6:9])+1 > w || uint24(chunk[3:6])*2+uint24(chunk[9:12])+1 > h {
				return 0, 0, bad
			}
		}
		if n&1 != 0 {
			if end >= len(data) || data[end] != 0 {
				return 0, 0, bad
			}
			end++
		}
		at = end
	}
	if w == 0 || h == 0 || animated && (!anim || !frame) || !animated && !imageData {
		return 0, 0, bad
	}
	return w, h, nil
}
func uint24(v []byte) int { return int(v[0]) | int(v[1])<<8 | int(v[2])<<16 }

func decodeStickerPrefix(data []byte, encoded bool) ([]byte, error) {
	data = bytes.Clone(data)
	if encoded {
		if len(data) < 128 {
			return nil, ErrInvalidStickerResource
		}
		key := []byte(stickerResourceKey)
		a, b, c := binary.BigEndian.Uint32(key[:4]), binary.BigEndian.Uint32(key[4:8]), binary.BigEndian.Uint32(key[8:12])
		for i := 0; i < 128; i++ {
			retainedB, retainedC := uint32(1), uint32(0)
			var stream byte
			for bit := 0; bit < 8; bit++ {
				control := a & 1
				a >>= 1
				if control != 0 {
					a ^= 0xc0000031
					retainedB = b & 1
					b = (b >> 1) & 0x3fffffff
					if retainedB != 0 {
						b ^= 0xe0000010
					}
				} else {
					retainedC = c & 1
					c = (c >> 1) & 0x0fffffff
					if retainedC != 0 {
						c ^= 0xf8000001
					}
				}
				stream = (stream << 1) | byte(retainedB^retainedC)
			}
			data[i] ^= stream
		}
	}
	return data, nil
}

func validStickerWebPFrame(data []byte) bool {
	found := false
	for at := 0; at < len(data); {
		if len(data)-at < 8 {
			return false
		}
		n := int(binary.LittleEndian.Uint32(data[at+4 : at+8]))
		end := at + 8 + n
		if end < at || end > len(data) {
			return false
		}
		payload := data[at+8 : end]
		switch string(data[at : at+4]) {
		case "VP8 ":
			if found || n < 10 || !bytes.Equal(payload[3:6], []byte{0x9d, 1, 0x2a}) {
				return false
			}
			found = true
		case "VP8L":
			if found || n < 5 || payload[0] != 0x2f {
				return false
			}
			found = true
		case "ALPH":
			if found || n < 1 {
				return false
			}
		default:
			return false
		}
		if n&1 != 0 {
			if end >= len(data) || data[end] != 0 {
				return false
			}
			end++
		}
		at = end
	}
	return found
}
