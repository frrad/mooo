package media

import (
	"encoding/json"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func syntheticAlbum(t *testing.T, n int) []Image {
	t.Helper()
	images := make([]Image, 0, n)
	for range n {
		image, err := PrepareImage(syntheticJPEG(t))
		if err != nil {
			t.Fatal(err)
		}
		images = append(images, image)
	}
	return images
}

func TestAlbumShipListsEachPhotoInOrder(t *testing.T) {
	images := syntheticAlbum(t, 2)
	body, err := (AlbumShipRequest{ChatID: 42, Images: images}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("c").Int64() != 42 || raw.Lookup("t").Int32() != MultiPhotoType || raw.Lookup("ex").StringValue() != "{}" {
		t.Fatalf("MSHIP = %v", raw)
	}
	var decoded struct {
		Sizes      []int64  `bson:"sl"`
		Checksums  []string `bson:"csl"`
		Extensions []string `bson:"el"`
	}
	if err := bson.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Sizes) != 2 || decoded.Sizes[0] != int64(len(images[0].Data)) || decoded.Checksums[1] != images[1].Checksum || decoded.Extensions[0] != images[0].Extension {
		t.Fatalf("MSHIP lists = %+v", decoded)
	}
}

func TestAlbumShipRejectsCountsOutsideKakaoLimits(t *testing.T) {
	for _, n := range []int{0, 1, MaxAlbumPhotos + 1} {
		images := make([]Image, n)
		for i := range images {
			images[i] = Image{Data: []byte("x"), Extension: "jpg", Checksum: "0000000000000000000000000000000000000000", Width: 1, Height: 1}
		}
		if _, err := (AlbumShipRequest{ChatID: 42, Images: images}).MarshalBSON(); !errors.Is(err, ErrInvalidAlbum) {
			t.Fatalf("%d photos: err = %v", n, err)
		}
	}
}

func TestDecodeAlbumShipResponseRequiresAlignedLists(t *testing.T) {
	good, _ := bson.Marshal(bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "kl", Value: bson.A{"k1", "k2"}}, {Key: "mtl", Value: bson.A{"image/jpeg", "image/jpeg"}},
		{Key: "vhl", Value: bson.A{"media1.invalid", "media2.invalid"}}, {Key: "pl", Value: bson.A{int32(995), int32(996)}},
	})
	response, err := DecodeAlbumShipResponse(good, 2)
	if err != nil {
		t.Fatal(err)
	}
	if response.Keys[1] != "k2" || response.Hosts[0] != "media1.invalid" || response.Ports[1] != 996 || response.MIMEs[0] != "image/jpeg" {
		t.Fatalf("response = %+v", response)
	}
	short, _ := bson.Marshal(bson.D{
		{Key: "kl", Value: bson.A{"k1"}}, {Key: "mtl", Value: bson.A{"image/jpeg", "image/jpeg"}},
		{Key: "vhl", Value: bson.A{"media1.invalid", "media2.invalid"}}, {Key: "pl", Value: bson.A{int32(995), int32(996)}},
	})
	if _, err := DecodeAlbumShipResponse(short, 2); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("misaligned lists err = %v", err)
	}
}

func TestAlbumItemPostCarriesNoChatOrExtra(t *testing.T) {
	image := syntheticAlbum(t, 1)[0]
	body, err := (AlbumPostRequest{UserID: 7, Key: "k1", Image: image, AppVersion: "26.8.0"}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("u").Int64() != 7 || raw.Lookup("k").StringValue() != "k1" || raw.Lookup("t").Int32() != MultiPhotoType ||
		raw.Lookup("s").Int64() != int64(len(image.Data)) || raw.Lookup("dt").Int32() != 2 || raw.Lookup("nt").Int32() != 0 {
		t.Fatalf("MPOST = %v", raw)
	}
	for _, absent := range []string{"c", "ex", "f", "mid"} {
		if _, err := raw.LookupErr(absent); err == nil {
			t.Fatalf("MPOST carries %s", absent)
		}
	}
}

func TestAlbumWriteExtraListsUploadedPhotos(t *testing.T) {
	images := syntheticAlbum(t, 2)
	ship := AlbumShipResponse{Keys: []string{"k1", "k2"}, MIMEs: []string{"image/jpeg", "image/jpeg"}}
	extra, err := AlbumWriteExtra(images, ship, "synthetic album caption")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Keys      []string `json:"kl"`
		Widths    []int32  `json:"wl"`
		Heights   []int32  `json:"hl"`
		MIMEs     []string `json:"mtl"`
		Sizes     []int64  `json:"sl"`
		Checksums []string `json:"csl"`
		Comments  []string `json:"cmtl"`
	}
	if err := json.Unmarshal([]byte(extra), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Keys) != 2 || decoded.Keys[0] != "k1" || decoded.Widths[1] != int32(images[1].Width) || decoded.Sizes[0] != int64(len(images[0].Data)) ||
		decoded.Checksums[1] != images[1].Checksum || decoded.MIMEs[1] != "image/jpeg" {
		t.Fatalf("extra = %s", extra)
	}
	if len(decoded.Comments) != 2 || decoded.Comments[0] != "synthetic album caption" || decoded.Comments[1] != "" {
		t.Fatalf("captions = %q", decoded.Comments)
	}
	plain, err := AlbumWriteExtra(images, ship, "")
	if err != nil {
		t.Fatal(err)
	}
	var withoutCaption map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plain), &withoutCaption); err != nil {
		t.Fatal(err)
	}
	if _, ok := withoutCaption["cmtl"]; ok {
		t.Fatal("uncaptioned album sends cmtl")
	}
}
