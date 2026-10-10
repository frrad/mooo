package connector

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
)

func avatarPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestAvatarIDDoesNotPersistRawURL(t *testing.T) {
	const raw = "https://talk.kakaocdn.net/avatar?secret=private"
	avatar := avatarFromURL(raw)
	if avatar == nil || avatar.ID == "" {
		t.Fatal("avatar source did not produce an ID")
	}
	if strings.Contains(string(avatar.ID), raw) || strings.Contains(string(avatar.ID), "private") {
		t.Fatalf("avatar ID leaked source URL: %q", avatar.ID)
	}
}

// The bridgev2 portal owns prior-MXC retention after an avatar failure. This
// connector test only proves that a failed upload remains retryable and that a
// later successful upload returns the new MXC.
func TestAvatarReuploadFailureCanRetry(t *testing.T) {
	data := avatarPNG(t)
	avatar := &bridgev2.Avatar{ID: "avatar-test", Get: func(context.Context) ([]byte, error) { return data, nil }}
	intent := &photoMatrixAPI{uploadErr: errors.New("upload failed")}
	_, hash, err := avatar.Reupload(context.Background(), intent, [32]byte{}, "mxc://previous")
	if err == nil || hash == [32]byte{} {
		t.Fatalf("failed upload result = uri/hash/error %v/%x/%v", "", hash, err)
	}
	intent.uploadErr = nil
	uri, retryHash, err := avatar.Reupload(context.Background(), intent, [32]byte{}, "mxc://previous")
	if err != nil || uri != "mxc://example/photo" || retryHash != hash {
		t.Fatalf("retry result = %q/%x/%v", uri, retryHash, err)
	}
}
