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
	"github.com/frrad/mooo/internal/protocol/media"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// A successful controlled upload returned 404 until the multipart filename
// matched the prepared resource's last component. Exercise the real Client
// photo path, including SHIP redirect, HTTP preparation/upload and WRITEMEDIA.
func TestClientPhotoRedirectUsesPreparedFilenameThenWritesOnce(t *testing.T) {
	data := syntheticClientJPEG(t)
	calls := 0
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
		write, err := peer.Read()
		if err != nil {
			return err
		}
		if write.Header.Method != "WRITEMEDIA" {
			return fmt.Errorf("completion method %s", write.Header.Method)
		}
		raw := bson.Raw(write.Body)
		photo := raw.Lookup("photo").Document()
		if photo.Lookup("resource").StringValue() != "synthetic/bucket/object/server-photo.jpg" || photo.Lookup("hfac").StringValue() != "synthetic0" || photo.Lookup("resourceType").StringValue() != "vk" {
			return fmt.Errorf("photo metadata differs")
		}
		var extra map[string]any
		if json.Unmarshal([]byte(raw.Lookup("extra").StringValue()), &extra) != nil || extra["cmt"] != "fixture caption" {
			return fmt.Errorf("caption missing")
		}
		calls++
		return peer.WriteBSON(write.Header.PacketID, "WRITEMEDIA", statusDocument(bson.E{Key: "chatId", Value: int64(42)}, bson.E{Key: "logId", Value: int64(99)}, bson.E{Key: "sendAt", Value: int32(1234)}))
	})
	s := &session{wire: clientWire(backend), nextID: 1, pushes: make(chan loco.Packet, 4), pending: make(map[uint32]pendingRequest), userID: 1, appVersion: "26.8.0", mediaDial: func(context.Context, string, int) (*wireConn, error) {
		t.Error("redirect dialed trailer")
		return nil, fmt.Errorf("unexpected trailer")
	}}
	done := make(chan struct{})
	go func() { defer close(done); s.readLoop() }()
	t.Cleanup(func() { _ = backend.Endpoint.Client.Close(); <-done })
	httpCalls := 0
	c := &Client{state: reusableTestState(), session: s, http: friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		httpCalls++
		if req.Method != "POST" {
			t.Fatalf("HTTP method %s", req.Method)
		}
		body := ""
		switch httpCalls {
		case 1:
			if req.URL.Host != "talk-pilsner.kakao.com" || req.URL.Path != "/messaging/files/v1/prepare" || req.Header.Get("Authorization") == "" {
				t.Fatal("incorrect preparation")
			}
			var obj map[string]any
			if json.NewDecoder(req.Body).Decode(&obj) != nil || obj["chatId"] != float64(42) || obj["size"] != float64(len(data)) || obj["extension"] != "jpg" || obj["mediaType"] != "image" {
				t.Fatal("prepare body differs")
			}
			body = `{"url":"https://kage.talk.kakao.com/offset-up/synthetic/ready/?credential=synthetic&signature=synthetic&expires=4102444800&finalize=true","resource":"synthetic/bucket/object/server-photo.jpg","resourceType":"vk","uploadKey":"synthetic0","chunkSize":4194304,"needReady":false}`
		case 2:
			if req.URL.Host != "kage.talk.kakao.com" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
				t.Fatal("upload host or credential isolation")
			}
			mr, err := req.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			offset, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			value, _ := io.ReadAll(offset)
			if offset.FormName() != "offset" || string(value) != "0" {
				t.Fatal("chunk offset differs")
			}
			file, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(file)
			if file.FormName() != "file_1" || file.FileName() != "server-photo.jpg" || !bytes.Equal(got, data) {
				t.Fatal("prepared filename or upload bytes differ")
			}
			body = fmt.Sprintf(`{"msg":"ok","info":[{"filename":"server-photo.jpg","length":%d,"content_type":"image"}]}`, len(data))
		default:
			t.Fatal("HTTP stage retried")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := c.SendImage(ctx, 42, data, "fixture caption")
	if err != nil {
		t.Fatalf("redirected photo failed: %v", err)
	}
	id, at, err := media.SendResultPosition(result)
	if err != nil || id != 99 || at != 1234 {
		t.Fatalf("position %d %d %v", id, at, err)
	}
	waitBackends(t, backend)
	if calls != 1 || httpCalls != 2 {
		t.Fatalf("write calls=%d HTTP calls=%d", calls, httpCalls)
	}
}

func TestClientDirectPhotoFailuresBeforeWriteAreCertainAndNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		prepareStatus, uploadStatus int
		ticket                      string
	}{
		{"prepare refusal", 503, 200, `{"url":"https://kage.talk.kakao.com/offset-up/a/b/","resource":"synthetic/server.jpg","resourceType":"vk","uploadKey":"synthetic0","chunkSize":4194304,"needReady":false}`},
		{"upload refusal", 200, 404, `{"url":"https://kage.talk.kakao.com/offset-up/a/b/","resource":"synthetic/server.jpg","resourceType":"vk","uploadKey":"synthetic0","chunkSize":4194304,"needReady":false}`},
		{"untraced Tenth strategy", 200, 200, `{"url":"https://kage.talk.kakao.com/offset-up/a/b/","resource":"synthetic/server.jpg","resourceType":"vt","uploadKey":"synthetic0","chunkSize":4194304,"needReady":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			backend := newScriptedBackend(t, true, func(peer *testloco.Peer) error {
				req, err := peer.Read()
				if err != nil {
					return err
				}
				if req.Header.Method != "SHIP" {
					return fmt.Errorf("expected SHIP")
				}
				if err = peer.WriteBSON(req.Header.PacketID, "SHIP", statusDocument(bson.E{Key: "rd", Value: true})); err != nil {
					return err
				}
				<-release
				return nil
			})
			s := &session{wire: clientWire(backend), nextID: 1, pushes: make(chan loco.Packet, 4), pending: make(map[uint32]pendingRequest)}
			done := make(chan struct{})
			go func() { defer close(done); s.readLoop() }()
			t.Cleanup(func() { _ = backend.Endpoint.Client.Close(); <-done })
			calls := 0
			c := &Client{state: reusableTestState(), session: s, http: friendDoerFunc(func(*http.Request) (*http.Response, error) {
				calls++
				status, body := tc.prepareStatus, tc.ticket
				if calls == 2 {
					status, body = tc.uploadStatus, `{"msg":"Not found upload ready key."}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.SendImage(ctx, 42, syntheticClientJPEG(t), "")
			close(release)
			waitBackends(t, backend)
			if !errors.Is(err, ErrPhotoNotCreated) {
				t.Fatalf("before-write failure %v", err)
			}
			wantCalls := 1
			if tc.name == "upload refusal" {
				wantCalls = 2
			}
			if calls != wantCalls || s.nextID != requestIDMin+1 {
				t.Fatalf("HTTP calls %d; next packet ID %d", calls, s.nextID)
			}
		})
	}
}

func TestClientDirectPhotoLostWriteReplyRemainsAmbiguous(t *testing.T) {
	writes := 0
	backend := newScriptedBackend(t, true, func(peer *testloco.Peer) error {
		req, err := peer.Read()
		if err != nil {
			return err
		}
		if err = peer.WriteBSON(req.Header.PacketID, "SHIP", statusDocument(bson.E{Key: "rd", Value: true})); err != nil {
			return err
		}
		req, err = peer.Read()
		if err != nil {
			return err
		}
		if req.Header.Method != "WRITEMEDIA" {
			return fmt.Errorf("expected WRITEMEDIA")
		}
		writes++
		return nil
	})
	s := &session{wire: clientWire(backend), nextID: 1, pushes: make(chan loco.Packet, 4), pending: make(map[uint32]pendingRequest)}
	done := make(chan struct{})
	go func() { defer close(done); s.readLoop() }()
	t.Cleanup(func() { _ = backend.Endpoint.Client.Close(); <-done })
	data := syntheticClientJPEG(t)
	calls := 0
	c := &Client{state: reusableTestState(), session: s, http: friendDoerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		body := `{"url":"https://kage.talk.kakao.com/offset-up/a/b/","resource":"synthetic/server.jpg","resourceType":"vk","uploadKey":"synthetic0","chunkSize":4194304,"needReady":false}`
		if calls == 2 {
			body = fmt.Sprintf(`{"msg":"ok","info":[{"filename":"server.jpg","length":%d,"content_type":"image"}]}`, len(data))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.SendImage(ctx, 42, data, "")
	waitBackends(t, backend)
	if err == nil || errors.Is(err, ErrPhotoNotCreated) || writes != 1 || calls != 2 {
		t.Fatalf("write outcome %v; writes=%d HTTP calls=%d", err, writes, calls)
	}
}
