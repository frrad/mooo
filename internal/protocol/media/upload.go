package media

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/unicode/norm"

	"github.com/frrad/mooo/internal/protocol/messagetype"
)

const (
	FileType  = messagetype.File
	VideoType = messagetype.Video
	// MaxUploadBytes bounds an outbound file or video. The Mac client allows
	// 300 MiB by default; mooo buffers uploads in memory and keeps its 64 MiB
	// receive policy for sends too.
	MaxUploadBytes = 64 << 20
	maxUploadName  = 512
)

var (
	ErrInvalidUpload       = errors.New("media: invalid upload")
	ErrUploadTooLarge      = errors.New("media: upload exceeds the size limit")
	ErrDeniedExtension     = errors.New("media: file extension is not allowed")
	ErrCaptionNotSupported = errors.New("media: KakaoTalk files carry no caption")
)

// videoExtensions are the extensions the Mac client sends as video; every
// other non-photo file, audio included, is sent as an ordinary file.
var videoExtensions = map[string]bool{
	"ts": true, "ogv": true, "flv": true, "mov": true, "mp4": true, "mpeg": true,
	"mpg": true, "mkv": true, "wmv": true, "asf": true, "avi": true, "m4v": true,
}

// deniedExtensions is the Mac client's built-in deny list, used when no
// server-provided list is stored. mooo has no server list.
var deniedExtensions = func() map[string]bool {
	set := map[string]bool{}
	for _, ext := range strings.Fields(`0xe 73k 89k a6p ac acc acr action actm ahk air apk app arscript as asb awk
		azw2 bat beam bin btm cel celx chm cmd cof com command cpl crt csh dek dld dmc docm dotm dxl ear ebm ebs
		ebs2 ecf eham elf es ex4 exe exopc ezs fas fileloc fky fpi frs fxp gadget gs ham hms hpf hta iim inc
		inetloc inf1 ins inx ipa ipf isp isu jar job js jse jsx kix ksh lnk lo ls mam mcr mel mpx mrc ms msc msi
		msp mst mxe nexe obs ore osx otm out paf pex php pif plx potm ppam ppsm pptm prc prg ps1 pvd pwc pyc pyo
		qpx rbx reg rgs rox rpj run s2a sbs sca scar scb scr script sct sh shb shs smm spr tcp thm tlb tms u3p
		udf upx url vb vbe vbs vbscript vlx vpm wcm webloc widget wiz workflow wpk wpm ws wsf xap xbap xlam xlm
		xlsm xltm xqt xys zl9`) {
		set[ext] = true
	}
	return set
}()

// Upload is one prepared outbound file or video.
type Upload struct {
	Type      int32
	Name      string
	Extension string
	Data      []byte
	Checksum  string
	Comment   string
}

// ClassifyUpload checks an outbound file's name and caption before its bytes
// are fetched. Like the Mac client it classifies by the lowercased
// extension: video extensions become video, everything else (audio included)
// an ordinary file. Only video carries a caption.
func ClassifyUpload(name, comment string) (Upload, error) {
	name = norm.NFC.String(name)
	if name == "" || name == "." || name == ".." || len(name) > maxUploadName || !utf8.ValidString(name) ||
		strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return Upload{}, ErrInvalidUpload
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if deniedExtensions[ext] {
		return Upload{}, ErrDeniedExtension
	}
	typ := FileType
	if videoExtensions[ext] {
		typ = VideoType
	}
	if typ == FileType && comment != "" {
		return Upload{}, ErrCaptionNotSupported
	}
	if !ValidCaption(comment) {
		return Upload{}, ErrInvalidCaption
	}
	return Upload{Type: typ, Name: name, Extension: ext, Comment: comment}, nil
}

// PrepareUpload validates an outbound file before any request.
func PrepareUpload(name string, data []byte, comment string) (Upload, error) {
	if len(data) == 0 {
		return Upload{}, ErrInvalidUpload
	}
	if len(data) > MaxUploadBytes {
		return Upload{}, ErrUploadTooLarge
	}
	upload, err := ClassifyUpload(name, comment)
	if err != nil {
		return Upload{}, err
	}
	sum := sha1.Sum(data)
	upload.Data = data
	upload.Checksum = strings.ToUpper(hex.EncodeToString(sum[:]))
	return upload, nil
}

func (u Upload) valid() bool {
	return (u.Type == FileType || u.Type == VideoType) && len(u.Data) > 0 && u.Name != "" && len(u.Checksum) == 40
}

type UploadShipRequest struct {
	ChatID int64
	Upload Upload
}

func (r UploadShipRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || !r.Upload.valid() {
		return nil, ErrInvalidUpload
	}
	return bson.Marshal(bson.D{
		{Key: "c", Value: r.ChatID}, {Key: "t", Value: r.Upload.Type},
		{Key: "s", Value: int64(len(r.Upload.Data))}, {Key: "cs", Value: r.Upload.Checksum},
		{Key: "e", Value: r.Upload.Extension}, {Key: "ex", Value: "{}"},
	})
}

type UploadPostRequest struct {
	UserID     int64
	Key        string
	ChatID     int64
	Upload     Upload
	AppVersion string
	MediaID    int64
}

// MarshalBSON builds the POST for a file or video. The server learns the
// file name only from f; dimensions are zero and no duration is sent.
func (r UploadPostRequest) MarshalBSON() ([]byte, error) {
	if r.UserID <= 0 || r.ChatID <= 0 || r.Key == "" || r.AppVersion == "" || r.MediaID <= 0 || !r.Upload.valid() {
		return nil, ErrInvalidUpload
	}
	extra := "{}"
	if r.Upload.Comment != "" {
		encoded, err := json.Marshal(struct {
			Comment string `json:"cmt"`
		}{r.Upload.Comment})
		if err != nil {
			return nil, ErrInvalidCaption
		}
		extra = string(encoded)
	}
	return bson.Marshal(bson.D{
		{Key: "u", Value: r.UserID}, {Key: "k", Value: r.Key}, {Key: "t", Value: r.Upload.Type},
		{Key: "s", Value: int64(len(r.Upload.Data))}, {Key: "c", Value: r.ChatID}, {Key: "mid", Value: r.MediaID},
		{Key: "w", Value: int32(0)}, {Key: "h", Value: int32(0)},
		{Key: "mm", Value: "99999"}, {Key: "nt", Value: int32(0)}, {Key: "os", Value: "mac"},
		{Key: "av", Value: r.AppVersion}, {Key: "ex", Value: extra}, {Key: "f", Value: r.Upload.Name},
		{Key: "ns", Value: false}, {Key: "dt", Value: int32(4)}, {Key: "scp", Value: int32(1)},
	})
}
