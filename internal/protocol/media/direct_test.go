package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type observedKageFixture struct {
	Provenance struct {
		Kind string `json:"kind"`
	} `json:"provenance"`
	PrepareResponseKeys []string `json:"prepare_response_keys"`
	Cases               []struct {
		Name            string `json:"name"`
		MediaType       string `json:"media_type"`
		Extension       string `json:"extension"`
		PartContentType string `json:"part_content_type"`
		Size            int    `json:"size"`
		Resource        string `json:"resource"`
		Prepare         struct {
			ResourceType string `json:"resourceType"`
			ChunkSize    int64  `json:"chunkSize"`
			NeedReady    bool   `json:"needReady"`
		} `json:"prepare"`
		Requests []struct {
			Offset int `json:"offset"`
			Bytes  int `json:"bytes"`
		} `json:"requests"`
		Responses []json.RawMessage `json:"responses"`
		Ready     []struct {
			PrevStatus int             `json:"prevStatus"`
			Response   json.RawMessage `json:"response"`
		} `json:"ready"`
	} `json:"cases"`
}

func loadObservedKageFixture(t *testing.T) observedKageFixture {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/media-upload/observed-kage-file-video.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture observedKageFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Provenance.Kind != "observed" || len(fixture.Cases) != 4 {
		t.Fatalf("unexpected fixture shape: %q %d", fixture.Provenance.Kind, len(fixture.Cases))
	}
	return fixture
}

// fixtureUpload prepares a synthetic upload of the case's type and size.
func fixtureUpload(t *testing.T, mediaType, extension string, size int, caption string) Upload {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7)
	}
	upload, err := PrepareUpload("synthetic upload."+extension, data, caption)
	if err != nil {
		t.Fatal(err)
	}
	want := messagetype.File
	if mediaType == "video" {
		want = messagetype.Video
	}
	if upload.Type != want {
		t.Fatalf("upload type %d, want %d", upload.Type, want)
	}
	return upload
}

// Each observed live exchange (2026-10-19, owned disposable account) is
// replayed through PrepareDirect and UploadDirect: the prepare body, every
// chunk's offset, size, filename and part type, and the server's
// acknowledgements, including a file's "etc/dat" info and a video's final
// reply without info.
func TestDirectUploadReplaysObservedFileAndVideoExchanges(t *testing.T) {
	fixture := loadObservedKageFixture(t)
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			upload := fixtureUpload(t, tc.MediaType, tc.Extension, tc.Size, "")
			m, err := UploadDirectMedia(upload)
			if err != nil {
				t.Fatal(err)
			}
			if m.MediaType != tc.MediaType || m.Extension != tc.Extension || m.MIMEType != tc.PartContentType {
				t.Fatalf("direct media %q %q %q", m.MediaType, m.Extension, m.MIMEType)
			}
			ticketBody, _ := json.Marshal(map[string]any{
				"url": "https://kage.talk.kakao.com/offset-up/synthetic/ready/?signature=synthetic", "resource": tc.Resource,
				"resourceType": tc.Prepare.ResourceType, "uploadKey": "synthetic0", "chunkSize": tc.Prepare.ChunkSize, "needReady": tc.Prepare.NeedReady,
			})
			prepared := 0
			ticket, err := PrepareDirect(context.Background(), directDoerFunc(func(req *http.Request) (*http.Response, error) {
				prepared++
				var body map[string]any
				if req.URL.String() != directPrepareURL || json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Fatal("prepare request")
				}
				want := map[string]any{"chatId": float64(42), "mediaType": tc.MediaType, "size": float64(tc.Size), "extension": tc.Extension}
				if !reflect.DeepEqual(body, want) {
					t.Fatalf("prepare body %v, want %v", body, want)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(ticketBody))}, nil
			}), directTestProfile(), 42, m)
			if err != nil || prepared != 1 || ticket.NeedReady != tc.Prepare.NeedReady {
				t.Fatalf("prepare err=%v calls=%d needReady=%v", err, prepared, ticket.NeedReady)
			}
			wantName := tc.Resource[strings.LastIndex(tc.Resource, "/")+1:]
			chunk := 0
			err = UploadDirect(context.Background(), directDoerFunc(func(req *http.Request) (*http.Response, error) {
				if chunk >= len(tc.Requests) {
					t.Fatal("more chunks than observed")
				}
				mr, err := req.MultipartReader()
				if err != nil {
					t.Fatal(err)
				}
				offsetPart, err := mr.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				offset, _ := io.ReadAll(offsetPart)
				filePart, err := mr.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(filePart)
				want := tc.Requests[chunk]
				if string(offset) != strconv.Itoa(want.Offset) || len(got) != want.Bytes || !bytes.Equal(got, upload.Data[want.Offset:want.Offset+want.Bytes]) {
					t.Fatalf("chunk %d: offset %s, %d bytes", chunk, offset, len(got))
				}
				if filePart.FileName() != wantName || filePart.Header.Get("Content-Type") != tc.PartContentType {
					t.Fatalf("chunk %d: filename %q type %q", chunk, filePart.FileName(), filePart.Header.Get("Content-Type"))
				}
				response := tc.Responses[chunk]
				chunk++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(response))}, nil
			}), directTestProfile(), ticket, m)
			if err != nil || chunk != len(tc.Requests) {
				t.Fatalf("upload err=%v chunks=%d", err, chunk)
			}
		})
	}
}

// The final acknowledgement must still be consistent when it identifies the
// upload, and a photo keeps requiring its "image" info.
func TestDirectUploadFinalAcknowledgementChecks(t *testing.T) {
	for _, tc := range []struct {
		name, mediaType, body string
		ok                    bool
	}{
		{"file info", "file", `{"msg":"ok","info":[{"filename":"server.dat","length":10,"content_type":"etc/dat"}]}`, true},
		{"video without info", "video", `{"msg":"ok"}`, true},
		{"file without info", "file", `{"msg":"ok"}`, true},
		{"file wrong length", "file", `{"msg":"ok","info":[{"filename":"server.dat","length":9,"content_type":"etc/dat"}]}`, false},
		{"video wrong filename", "video", `{"msg":"ok","info":[{"filename":"other.mp4","length":10}]}`, false},
		{"empty info", "file", `{"msg":"ok","info":[]}`, false},
		{"not ok", "video", `{"msg":"Not found upload ready key."}`, false},
		{"photo without info", "image", `{"msg":"ok"}`, false},
		{"photo other content type", "image", `{"msg":"ok","info":[{"filename":"server.dat","length":10,"content_type":"etc/dat"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeDirectUpload([]byte(tc.body), tc.mediaType, "server.dat", 10, 10, true)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// The static fixture holds the file and video WRITEMEDIA on which the Mac and
// Android serializers agree: file adds name and lowercase checksum, video
// only the resource triple and a caption in extra.
func TestDirectUploadWriteMatchesStaticFixture(t *testing.T) {
	var fixture struct {
		Provenance struct {
			Kind string `json:"kind"`
		} `json:"provenance"`
		WriteCases []struct {
			Name  string `json:"name"`
			Input struct {
				ChatID       int64  `json:"chat_id"`
				MessageID    int64  `json:"message_id"`
				Type         int32  `json:"type"`
				Resource     string `json:"resource"`
				ResourceType string `json:"resource_type"`
				UploadKey    string `json:"upload_key"`
				Name         string `json:"name"`
				SHA1Hex      string `json:"sha1_hex"`
				Caption      string `json:"caption"`
			} `json:"input"`
			Expected struct {
				Type     int32             `json:"type"`
				MediaKey string            `json:"media_key"`
				Media    map[string]any    `json:"media"`
				Extra    map[string]string `json:"extra"`
			} `json:"expected"`
		} `json:"write_cases"`
	}
	raw, err := os.ReadFile("../../../research/fixtures/media-upload/static-write-file-video.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Provenance.Kind != "static" || len(fixture.WriteCases) != 2 {
		t.Fatalf("unexpected fixture shape")
	}
	for _, tc := range fixture.WriteCases {
		t.Run(tc.Name, func(t *testing.T) {
			in := tc.Input
			upload := Upload{Type: in.Type, Name: in.Name, Extension: "dat", Data: []byte("synthetic"), Checksum: strings.ToUpper(in.SHA1Hex), Comment: in.Caption}
			if in.Type == messagetype.Video {
				upload.Name, upload.Extension, upload.Checksum = "synthetic.mp4", "mp4", strings.Repeat("A", 40)
			}
			body, err := (DirectUploadWriteRequest{ChatID: in.ChatID, MessageID: in.MessageID, Upload: upload,
				Ticket: DirectTicket{Resource: in.Resource, ResourceType: in.ResourceType, UploadKey: in.UploadKey}}).MarshalBSON()
			if err != nil {
				t.Fatal(err)
			}
			doc := bson.Raw(body)
			var media map[string]any
			if err := bson.Unmarshal(doc.Lookup(tc.Expected.MediaKey).Document(), &media); err != nil {
				t.Fatalf("media key %q: %v", tc.Expected.MediaKey, err)
			}
			if !reflect.DeepEqual(media, tc.Expected.Media) {
				t.Fatalf("media %v, want %v", media, tc.Expected.Media)
			}
			if doc.Lookup("type").Int32() != tc.Expected.Type || doc.Lookup("chatId").Int64() != in.ChatID || doc.Lookup("msgId").Int64() != in.MessageID {
				t.Fatal("top-level fields differ")
			}
			extraValue, hasExtra := doc.Lookup("extra").StringValueOK()
			if tc.Expected.Extra == nil {
				if hasExtra {
					t.Fatalf("unexpected extra %q", extraValue)
				}
				return
			}
			var extra map[string]string
			if !hasExtra || json.Unmarshal([]byte(extraValue), &extra) != nil || !reflect.DeepEqual(extra, tc.Expected.Extra) {
				t.Fatalf("extra %q, want %v", extraValue, tc.Expected.Extra)
			}
		})
	}
}

// The ready poll sends the static fixture's fields and recognizes the
// content filter's refusal; any other refusal is an HTTP status error.
func TestCheckDirectReadyRequestAndRefusals(t *testing.T) {
	ticket := DirectTicket{URL: "https://kage.talk.kakao.com/offset-up/a/", Resource: "synthetic/server.mp4", ResourceType: "vk", UploadKey: "synthetic0", ChunkSize: 4194304, NeedReady: true}
	var got map[string]any
	ready, err := CheckDirectReady(context.Background(), directDoerFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != directReadyURL || req.Header.Get("Authorization") == "" || json.NewDecoder(req.Body).Decode(&got) != nil {
			t.Fatal("ready request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":5,"interval":1000,"maxWaitSeconds":600}`))}, nil
	}), directTestProfile(), 42, "video", ticket, 5, "synthetic-job")
	want := map[string]any{"resource": "synthetic/server.mp4", "resourceType": "vk", "chatId": float64(42), "mediaType": "video", "uploadKey": "synthetic0", "prevStatus": float64(5), "jobId": "synthetic-job"}
	if err != nil || !reflect.DeepEqual(got, want) || ready != (DirectReady{Status: 5, IntervalMS: 1000, MaxWaitSeconds: 600}) {
		t.Fatalf("ready=%+v err=%v body=%v", ready, err, got)
	}
	for _, tc := range []struct {
		name, body string
		filtered   bool
	}{
		{"content filter", `{"code":"90025","status":400,"message":"synthetic"}`, true},
		{"numeric content filter code", `{"code":90025,"status":400}`, true},
		{"other refusal", `{"code":"90001","status":400}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CheckDirectReady(context.Background(), directDoerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}), directTestProfile(), 42, "video", ticket, 0, "")
			if errors.Is(err, ErrUploadFiltered) != tc.filtered || err == nil {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// A file without an extension cannot use the HTTP upload, and file and video
// part types follow the research.
func TestUploadDirectMediaTypes(t *testing.T) {
	for _, tc := range []struct {
		name, mediaType, mimeType string
		err                       error
	}{
		{"clip.MOV", "video", "video/quicktime", nil},
		{"clip.mp4", "video", "video/mp4", nil},
		{"report.pdf", "file", "application/octet-stream", nil},
		{"README", "", "", ErrUploadRedirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upload, err := PrepareUpload(tc.name, []byte("synthetic"), "")
			if err != nil {
				t.Fatal(err)
			}
			m, err := UploadDirectMedia(upload)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err == nil && (m.MediaType != tc.mediaType || m.MIMEType != tc.mimeType) {
				t.Fatalf("media %q %q", m.MediaType, m.MIMEType)
			}
		})
	}
}
