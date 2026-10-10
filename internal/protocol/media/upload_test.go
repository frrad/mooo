package media

import (
	"bytes"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPrepareUploadClassifiesByExtensionLikeTheMacClient(t *testing.T) {
	cases := []struct {
		name     string
		wantType int32
		wantExt  string
	}{
		{"synthetic.txt", FileType, "txt"},
		{"Synthetic.PDF", FileType, "pdf"},
		{"voice.m4a", FileType, "m4a"},
		{"clip.MP4", VideoType, "mp4"},
		{"clip.mov", VideoType, "mov"},
		{"no-extension", FileType, ""},
	}
	for _, tc := range cases {
		upload, err := PrepareUpload(tc.name, []byte("synthetic bytes"), "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if upload.Type != tc.wantType || upload.Extension != tc.wantExt || upload.Name != tc.name {
			t.Fatalf("%s: got type=%d ext=%q name=%q", tc.name, upload.Type, upload.Extension, upload.Name)
		}
		if upload.Checksum != "5B785ED3E6E59C9CAB851B30C5224AD3846893DF" {
			t.Fatalf("%s: checksum %q", tc.name, upload.Checksum)
		}
	}
}

func TestPrepareUploadNormalizesTheFileNameToNFC(t *testing.T) {
	decomposed := "café.txt"
	upload, err := PrepareUpload(decomposed, []byte("x"), "")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Name != "café.txt" {
		t.Fatalf("name = %q, want NFC", upload.Name)
	}
}

func TestPrepareUploadRejectsBeforeAnySend(t *testing.T) {
	cases := []struct {
		label   string
		name    string
		data    []byte
		comment string
		want    error
	}{
		{"empty", "synthetic.txt", nil, "", ErrInvalidUpload},
		{"too large", "synthetic.txt", make([]byte, MaxUploadBytes+1), "", ErrUploadTooLarge},
		{"denied executable", "synthetic.EXE", []byte("x"), "", ErrDeniedExtension},
		{"denied macro document", "synthetic.docm", []byte("x"), "", ErrDeniedExtension},
		{"path separator", "dir/synthetic.txt", []byte("x"), "", ErrInvalidUpload},
		{"control character", "synthetic\n.txt", []byte("x"), "", ErrInvalidUpload},
		{"empty name", "", []byte("x"), "", ErrInvalidUpload},
		{"file caption", "synthetic.txt", []byte("x"), "caption", ErrCaptionNotSupported},
		{"video caption too long", "clip.mp4", []byte("x"), string(bytes.Repeat([]byte("a"), MaxCaptionBytes+1)), ErrInvalidCaption},
	}
	for _, tc := range cases {
		if _, err := PrepareUpload(tc.name, tc.data, tc.comment); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.label, err, tc.want)
		}
	}
}

func TestUploadShipAndPostCarryTypeNameAndCaption(t *testing.T) {
	upload, err := PrepareUpload("clip.mp4", []byte("synthetic video"), "synthetic caption")
	if err != nil {
		t.Fatal(err)
	}
	ship, err := (UploadShipRequest{ChatID: 42, Upload: upload}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(ship)
	if raw.Lookup("c").Int64() != 42 || raw.Lookup("t").Int32() != VideoType || raw.Lookup("s").Int64() != int64(len("synthetic video")) ||
		raw.Lookup("cs").StringValue() != upload.Checksum || raw.Lookup("e").StringValue() != "mp4" || raw.Lookup("ex").StringValue() != "{}" {
		t.Fatalf("SHIP = %v", raw)
	}
	post, err := (UploadPostRequest{UserID: 7, Key: "synthetic-ticket", ChatID: 42, Upload: upload, AppVersion: "26.8.0", MediaID: 9}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw = bson.Raw(post)
	if raw.Lookup("t").Int32() != VideoType || raw.Lookup("f").StringValue() != "clip.mp4" || raw.Lookup("k").StringValue() != "synthetic-ticket" ||
		raw.Lookup("w").Int32() != 0 || raw.Lookup("h").Int32() != 0 || raw.Lookup("ex").StringValue() != `{"cmt":"synthetic caption"}` ||
		raw.Lookup("s").Int64() != int64(len("synthetic video")) || raw.Lookup("c").Int64() != 42 || raw.Lookup("u").Int64() != 7 {
		t.Fatalf("POST = %v", raw)
	}

	file, err := PrepareUpload("synthetic.txt", []byte("synthetic file"), "")
	if err != nil {
		t.Fatal(err)
	}
	post, err = (UploadPostRequest{UserID: 7, Key: "synthetic-ticket", ChatID: 42, Upload: file, AppVersion: "26.8.0", MediaID: 9}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw = bson.Raw(post)
	if raw.Lookup("t").Int32() != FileType || raw.Lookup("f").StringValue() != "synthetic.txt" || raw.Lookup("ex").StringValue() != "{}" {
		t.Fatalf("file POST = %v", raw)
	}
}
