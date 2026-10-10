package media

import (
	"encoding/json"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/messagetype"
)

const (
	MultiPhotoType = messagetype.MultiPhoto
	// MinAlbumPhotos: the Mac client never creates a one-photo album, and its
	// hard-coded maximum is MaxAlbumPhotos.
	MinAlbumPhotos     = 2
	AlbumShipCommand   = "MSHIP"
	AlbumPostCommand   = "MPOST"
	albumPostDeviceTyp = int32(2)
)

var ErrInvalidAlbum = errors.New("media: invalid album")

// AlbumShipRequest reserves upload slots for every photo of one album.
type AlbumShipRequest struct {
	ChatID int64
	Images []Image
}

func (r AlbumShipRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || len(r.Images) < MinAlbumPhotos || len(r.Images) > MaxAlbumPhotos {
		return nil, ErrInvalidAlbum
	}
	sizes := make(bson.A, 0, len(r.Images))
	checksums := make(bson.A, 0, len(r.Images))
	extensions := make(bson.A, 0, len(r.Images))
	for _, image := range r.Images {
		if len(image.Data) == 0 || image.Extension == "" || len(image.Checksum) != 40 {
			return nil, ErrInvalidAlbum
		}
		sizes = append(sizes, int64(len(image.Data)))
		checksums = append(checksums, image.Checksum)
		extensions = append(extensions, image.Extension)
	}
	return bson.Marshal(bson.D{
		{Key: "c", Value: r.ChatID}, {Key: "t", Value: MultiPhotoType},
		{Key: "sl", Value: sizes}, {Key: "csl", Value: checksums}, {Key: "el", Value: extensions},
		{Key: "ex", Value: "{}"},
	})
}

// AlbumShipResponse holds one upload token, MIME type and media endpoint per
// photo, in request order.
type AlbumShipResponse struct {
	Keys  []string
	MIMEs []string
	Hosts []string
	Ports []int
}

func DecodeAlbumShipResponse(body []byte, count int) (AlbumShipResponse, error) {
	raw := bson.Raw(body)
	keys, err := stringList(raw, "kl", count)
	if err != nil {
		return AlbumShipResponse{}, err
	}
	mimes, err := stringList(raw, "mtl", count)
	if err != nil {
		return AlbumShipResponse{}, err
	}
	hosts, err := stringList(raw, "vhl", count)
	if err != nil {
		return AlbumShipResponse{}, err
	}
	value, err := raw.LookupErr("pl")
	if err != nil || value.Type != bson.TypeArray {
		return AlbumShipResponse{}, ErrInvalidResponse
	}
	elements, err := value.Array().Values()
	if err != nil || len(elements) != count {
		return AlbumShipResponse{}, ErrInvalidResponse
	}
	ports := make([]int, 0, count)
	for _, element := range elements {
		port, err := integerValue(element)
		if err != nil || port <= 0 || port > 65535 {
			return AlbumShipResponse{}, ErrInvalidResponse
		}
		ports = append(ports, int(port))
	}
	return AlbumShipResponse{Keys: keys, MIMEs: mimes, Hosts: hosts, Ports: ports}, nil
}

func stringList(raw bson.Raw, key string, count int) ([]string, error) {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeArray {
		return nil, ErrInvalidResponse
	}
	elements, err := value.Array().Values()
	if err != nil || len(elements) != count {
		return nil, ErrInvalidResponse
	}
	out := make([]string, 0, count)
	for _, element := range elements {
		text, ok := element.StringValueOK()
		if !ok || text == "" {
			return nil, ErrInvalidResponse
		}
		out = append(out, text)
	}
	return out, nil
}

// AlbumPostRequest uploads one album photo. Unlike a single photo it carries
// no chat ID, extra JSON or file name.
type AlbumPostRequest struct {
	UserID     int64
	Key        string
	Image      Image
	AppVersion string
}

func (r AlbumPostRequest) MarshalBSON() ([]byte, error) {
	if r.UserID <= 0 || r.Key == "" || r.AppVersion == "" || len(r.Image.Data) == 0 {
		return nil, ErrInvalidAlbum
	}
	return bson.Marshal(bson.D{
		{Key: "u", Value: r.UserID}, {Key: "k", Value: r.Key}, {Key: "t", Value: MultiPhotoType},
		{Key: "s", Value: int64(len(r.Image.Data))}, {Key: "scp", Value: int32(1)},
		{Key: "mm", Value: "99999"}, {Key: "nt", Value: int32(0)}, {Key: "os", Value: "mac"},
		{Key: "av", Value: r.AppVersion}, {Key: "dt", Value: albumPostDeviceTyp},
	})
}

// AlbumWriteExtra builds the WRITE extra that creates the album message after
// every photo is uploaded. A caption is attached to the first photo. The Mac
// client also sends its local file paths as imageUrls; mooo has none and
// omits them.
func AlbumWriteExtra(images []Image, ship AlbumShipResponse, caption string) (string, error) {
	n := len(images)
	if n < MinAlbumPhotos || n > MaxAlbumPhotos || len(ship.Keys) != n || len(ship.MIMEs) != n || !ValidCaption(caption) {
		return "", ErrInvalidAlbum
	}
	extra := struct {
		Keys      []string `json:"kl"`
		Widths    []int32  `json:"wl"`
		Heights   []int32  `json:"hl"`
		MIMEs     []string `json:"mtl"`
		Sizes     []int64  `json:"sl"`
		Checksums []string `json:"csl"`
		Comments  []string `json:"cmtl,omitempty"`
	}{Keys: ship.Keys, MIMEs: ship.MIMEs}
	for _, image := range images {
		extra.Widths = append(extra.Widths, int32(image.Width))
		extra.Heights = append(extra.Heights, int32(image.Height))
		extra.Sizes = append(extra.Sizes, int64(len(image.Data)))
		extra.Checksums = append(extra.Checksums, image.Checksum)
	}
	if caption != "" {
		extra.Comments = make([]string, n)
		extra.Comments[0] = caption
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		return "", ErrInvalidAlbum
	}
	return string(encoded), nil
}
