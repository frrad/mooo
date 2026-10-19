package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// redirectedUploadSession answers SHIP with rd=true, then hands the next
// LOCO request to write (nil means no WRITEMEDIA may arrive).
func redirectedUploadSession(t *testing.T, write func(loco.Packet, *testloco.Peer) error) (*session, func()) {
	t.Helper()
	release := make(chan struct{})
	backend := newScriptedBackend(t, true, func(peer *testloco.Peer) error {
		ship, err := peer.Read()
		if err != nil {
			return err
		}
		if ship.Header.Method != "SHIP" {
			return fmt.Errorf("first method %s", ship.Header.Method)
		}
		if err = peer.WriteBSON(ship.Header.PacketID, "SHIP", statusDocument(bson.E{Key: "rd", Value: true}, bson.E{Key: "k", Value: ""}, bson.E{Key: "vh", Value: ""}, bson.E{Key: "p", Value: int32(0)})); err != nil {
			return err
		}
		if write == nil {
			<-release
			return nil
		}
		request, err := peer.Read()
		if err != nil {
			return err
		}
		return write(request, peer)
	})
	s := &session{wire: clientWire(backend), nextID: 1, pushes: make(chan loco.Packet, 4), pending: make(map[uint32]pendingRequest), userID: 1, appVersion: "26.8.0", mediaDial: func(context.Context, string, int) (*wireConn, error) {
		t.Error("redirect dialed the trailer")
		return nil, fmt.Errorf("unexpected trailer")
	}}
	done := make(chan struct{})
	go func() { defer close(done); s.readLoop() }()
	t.Cleanup(func() { _ = backend.Endpoint.Client.Close(); <-done })
	return s, func() { close(release); waitBackends(t, backend) }
}

func directResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

const redirectedTicket = `{"url":"https://kage.talk.kakao.com/offset-up/synthetic/ready/?credential=synthetic&signature=synthetic","resource":"synthetic/bucket/object/%s","resourceType":"vk","uploadKey":"synthetic0","chunkSize":4194304,"needReady":%t}`

// Regression: a redirected file was refused with "upload method not
// supported". The real Client.SendUpload now prepares mediaType "file",
// uploads with the prepared filename, and sends one WRITEMEDIA type 18 with
// the file's name and lowercase SHA-1 checksum.
func TestClientSendUploadRedirectedFileUsesHTTPUploadThenWritesOnce(t *testing.T) {
	data := []byte("synthetic redirected file bytes")
	upload, err := media.PrepareUpload("synthetic report.pdf", data, "")
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	s, finish := redirectedUploadSession(t, func(write loco.Packet, peer *testloco.Peer) error {
		if write.Header.Method != "WRITEMEDIA" {
			return fmt.Errorf("completion method %s", write.Header.Method)
		}
		raw := bson.Raw(write.Body)
		file := raw.Lookup("file").Document()
		if raw.Lookup("type").Int32() != messagetype.File || file.Lookup("resource").StringValue() != "synthetic/bucket/object/server-file.pdf" ||
			file.Lookup("hfac").StringValue() != "synthetic0" || file.Lookup("name").StringValue() != "synthetic report.pdf" ||
			file.Lookup("checksum").StringValue() != strings.ToLower(upload.Checksum) {
			return fmt.Errorf("file metadata differs: %s", raw)
		}
		writes++
		return peer.WriteBSON(write.Header.PacketID, "WRITEMEDIA", statusDocument(bson.E{Key: "chatId", Value: int64(42)}, bson.E{Key: "logId", Value: int64(99)}, bson.E{Key: "sendAt", Value: int32(1234)}))
	})
	httpCalls := 0
	c := &Client{state: reusableTestState(), session: s, http: friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		httpCalls++
		switch httpCalls {
		case 1:
			var obj map[string]any
			if req.URL.Path != "/messaging/files/v1/prepare" || json.NewDecoder(req.Body).Decode(&obj) != nil || obj["mediaType"] != "file" || obj["extension"] != "pdf" || obj["size"] != float64(len(data)) {
				t.Fatalf("prepare differs: %v", obj)
			}
			return directResponse(fmt.Sprintf(redirectedTicket, "server-file.pdf", false)), nil
		case 2:
			mr, err := req.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = mr.NextPart(); err != nil {
				t.Fatal(err)
			}
			part, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(part)
			if part.FileName() != "server-file.pdf" || part.Header.Get("Content-Type") != "application/octet-stream" || !bytes.Equal(got, data) {
				t.Fatal("upload part differs")
			}
			return directResponse(fmt.Sprintf(`{"msg":"ok","info":[{"filename":"server-file.pdf","length":%d,"content_type":"etc/pdf"}]}`, len(data))), nil
		}
		t.Fatal("HTTP stage retried")
		return nil, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := c.SendUpload(ctx, 42, upload)
	if err != nil {
		t.Fatalf("redirected file failed: %v", err)
	}
	finish()
	if id, at, err := media.SendResultPosition(result); err != nil || id != 99 || at != 1234 {
		t.Fatalf("position %d %d %v", id, at, err)
	}
	if writes != 1 || httpCalls != 2 {
		t.Fatalf("writes=%d HTTP calls=%d", writes, httpCalls)
	}
}

// A redirected video with needReady polls ready (status 5, then 1) with
// prevStatus 0 and then the last status, before one WRITEMEDIA type 3 with
// the resource triple and the caption in extra, as observed live.
func TestClientSendUploadRedirectedVideoWaitsForReadyThenWrites(t *testing.T) {
	oldInterval := directReadyInterval
	directReadyInterval = time.Millisecond
	t.Cleanup(func() { directReadyInterval = oldInterval })
	data := []byte("synthetic redirected video bytes")
	upload, err := media.PrepareUpload("clip.mp4", data, "synthetic video caption")
	if err != nil {
		t.Fatal(err)
	}
	s, finish := redirectedUploadSession(t, func(write loco.Packet, peer *testloco.Peer) error {
		raw := bson.Raw(write.Body)
		video := raw.Lookup("video").Document()
		elements, _ := video.Elements()
		var extra map[string]string
		if write.Header.Method != "WRITEMEDIA" || raw.Lookup("type").Int32() != messagetype.Video || len(elements) != 3 ||
			video.Lookup("resource").StringValue() != "synthetic/bucket/object/server-video.mp4" ||
			json.Unmarshal([]byte(raw.Lookup("extra").StringValue()), &extra) != nil || extra["cmt"] != "synthetic video caption" {
			return fmt.Errorf("video write differs: %s", raw)
		}
		return peer.WriteBSON(write.Header.PacketID, "WRITEMEDIA", statusDocument(bson.E{Key: "chatId", Value: int64(42)}, bson.E{Key: "logId", Value: int64(100)}, bson.E{Key: "sendAt", Value: int32(1235)}))
	})
	var prevStatuses []any
	httpCalls := 0
	c := &Client{state: reusableTestState(), session: s, http: friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		httpCalls++
		switch {
		case req.URL.Path == "/messaging/files/v1/prepare":
			var obj map[string]any
			if json.NewDecoder(req.Body).Decode(&obj) != nil || obj["mediaType"] != "video" || obj["extension"] != "mp4" {
				t.Fatalf("prepare differs: %v", obj)
			}
			return directResponse(fmt.Sprintf(redirectedTicket, "server-video.mp4", true)), nil
		case req.URL.Host == "kage.talk.kakao.com":
			mr, err := req.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = mr.NextPart()
			part, _ := mr.NextPart()
			if part.Header.Get("Content-Type") != "video/mp4" {
				t.Fatal("video part type")
			}
			return directResponse(`{"msg":"ok"}`), nil
		case req.URL.Path == "/messaging/files/v1/ready":
			var obj map[string]any
			if json.NewDecoder(req.Body).Decode(&obj) != nil || obj["mediaType"] != "video" || obj["uploadKey"] != "synthetic0" {
				t.Fatalf("ready differs: %v", obj)
			}
			prevStatuses = append(prevStatuses, obj["prevStatus"])
			status := 5
			if len(prevStatuses) == 2 {
				status = 1
			}
			return directResponse(fmt.Sprintf(`{"status":%d,"interval":1,"maxWaitSeconds":600}`, status)), nil
		}
		t.Fatalf("unexpected HTTP %s", req.URL.Path)
		return nil, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := c.SendUpload(ctx, 42, upload)
	if err != nil {
		t.Fatalf("redirected video failed: %v", err)
	}
	finish()
	if id, _, err := media.SendResultPosition(result); err != nil || id != 100 {
		t.Fatalf("position %d %v", id, err)
	}
	if fmt.Sprint(prevStatuses) != "[0 5]" || httpCalls != 4 {
		t.Fatalf("ready prevStatus %v, HTTP calls %d", prevStatuses, httpCalls)
	}
}

// Every failure before WRITEMEDIA is certainly not sent and is not retried:
// the content filter, a ready deadline, a Tenth strategy and a refused chunk.
func TestClientSendUploadRedirectFailuresBeforeWriteAreNotSent(t *testing.T) {
	oldInterval := directReadyInterval
	directReadyInterval = time.Millisecond
	t.Cleanup(func() { directReadyInterval = oldInterval })
	for _, tc := range []struct {
		name      string
		ticket    string
		ready     func() *http.Response
		upload    int
		want      error
		wantCalls int
	}{
		{
			name: "content filter", ticket: fmt.Sprintf(redirectedTicket, "server-video.mp4", true), upload: 200,
			ready: func() *http.Response {
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"code":"90025","status":400}`))}
			},
			want: media.ErrUploadFiltered, wantCalls: 3,
		},
		{
			name: "ready deadline", ticket: fmt.Sprintf(redirectedTicket, "server-video.mp4", true), upload: 200,
			ready: func() *http.Response { return directResponse(`{"status":5,"interval":2000,"maxWaitSeconds":1}`) },
			want:  errDirectReadyTimeout, wantCalls: 3,
		},
		{
			name: "Tenth strategy", ticket: strings.Replace(fmt.Sprintf(redirectedTicket, "server-video.mp4", false), `"vk"`, `"vt"`, 1),
			want: media.ErrUploadRedirect, wantCalls: 1,
		},
		{
			name: "refused chunk", ticket: fmt.Sprintf(redirectedTicket, "server-video.mp4", false), upload: 404,
			want: macweb.ErrStatus, wantCalls: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upload, err := media.PrepareUpload("clip.mp4", []byte("synthetic video"), "")
			if err != nil {
				t.Fatal(err)
			}
			s, finish := redirectedUploadSession(t, nil)
			calls := 0
			c := &Client{state: reusableTestState(), session: s, http: friendDoerFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				switch {
				case req.URL.Path == "/messaging/files/v1/prepare":
					return directResponse(tc.ticket), nil
				case req.URL.Host == "kage.talk.kakao.com":
					return &http.Response{StatusCode: tc.upload, Body: io.NopCloser(strings.NewReader(`{"msg":"ok"}`))}, nil
				}
				return tc.ready(), nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err = c.SendUpload(ctx, 42, upload)
			finish()
			if !errors.Is(err, ErrUploadNotSent) {
				t.Fatalf("err = %v, want ErrUploadNotSent", err)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if calls != tc.wantCalls || s.nextID != requestIDMin+1 {
				t.Fatalf("HTTP calls %d, want %d; next packet ID %d", calls, tc.wantCalls, s.nextID)
			}
		})
	}
}
