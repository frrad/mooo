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

	"github.com/frrad/mooo/internal/protocol/macweb"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type directDoerFunc func(*http.Request) (*http.Response, error)

func (f directDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
func directTestProfile() macweb.Profile {
	return macweb.Profile{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en", AccessToken: "synthetic-access", DeviceUUID: "synthetic-device", UserID: 7}
}

func TestDirectPhotoWriteExecutedModel(t *testing.T) {
	var fixture struct {
		Input struct {
			ChatID        int64  `json:"chat_id"`
			MessageID     int64  `json:"message_id"`
			Resource      string `json:"resource"`
			ResourceType  string `json:"resource_type"`
			UploadKey     string `json:"upload_key"`
			Width, Height int32
			Caption       string
		}
		Command  string
		Expected map[string]any
	}
	raw, err := os.ReadFile("../../../research/fixtures/photo/executed-write-photo.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	in := fixture.Input
	body, err := (DirectPhotoWriteRequest{ChatID: in.ChatID, MessageID: in.MessageID, Image: Image{Width: in.Width, Height: in.Height}, Ticket: PhotoTicket{Resource: in.Resource, ResourceType: in.ResourceType, UploadKey: in.UploadKey}, Caption: in.Caption}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc bson.M
	if err = bson.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(doc)
	var got map[string]any
	if err = json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if fixture.Command != WriteMediaCommand || !reflect.DeepEqual(got, fixture.Expected) {
		t.Fatalf("write differs from executed model: %s", encoded)
	}
	var consumer struct{ Expected map[string]any }
	raw, err = os.ReadFile("../../../research/fixtures/photo/executed-photo-write-data.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &consumer); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["photo"], consumer.Expected) {
		t.Fatal("photo data differs from executed consumer")
	}
}

func TestDirectPhotoUploadObservedFilename(t *testing.T) {
	var fixture struct {
		Resource         string
		ExpectedFilename string `json:"expected_multipart_filename"`
		Accepted         struct {
			Status int
			Body   json.RawMessage
		} `json:"accepted_response"`
	}
	raw, err := os.ReadFile("../../../research/fixtures/photo/observed-kage-filename.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	ticket := PhotoTicket{URL: "https://kage.talk.kakao.com/offset-up/synthetic/ready/?signature=synthetic", Resource: fixture.Resource, ResourceType: "vk", UploadKey: "synthetic0", ChunkSize: 4194304}
	data := []byte("synthetic image bytes")
	doer := directDoerFunc(func(req *http.Request) (*http.Response, error) {
		mr, err := req.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		offset, err := mr.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		value, _ := io.ReadAll(offset)
		if string(value) != "0" {
			t.Fatal("initial offset")
		}
		file, err := mr.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(file)
		if file.FileName() != fixture.ExpectedFilename || !bytes.Equal(got, data) {
			t.Fatal("resource filename/bytes differ")
		}
		return &http.Response{StatusCode: fixture.Accepted.Status, Body: io.NopCloser(bytes.NewReader(fixture.Accepted.Body))}, nil
	})
	if err := UploadDirectPhoto(context.Background(), doer, directTestProfile(), ticket, Image{Data: data, Extension: "jpg"}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectPhotoRejectsUnsafeTicketBeforeUpload(t *testing.T) {
	base := PhotoTicket{URL: "https://kage.talk.kakao.com/offset-up/synthetic/ready/", Resource: "synthetic/photo.jpg", ResourceType: "vk", UploadKey: "synthetic0", ChunkSize: 4194304}
	for name, change := range map[string]func(*PhotoTicket){"http": func(t *PhotoTicket) { t.URL = "http://kage.talk.kakao.com/offset-up/a/" }, "other host": func(t *PhotoTicket) { t.URL = "https://example.com/offset-up/a/" }, "userinfo": func(t *PhotoTicket) { t.URL = "https://user@kage.talk.kakao.com/offset-up/a/" }, "fragment": func(t *PhotoTicket) { t.URL += "#secret" }, "route": func(t *PhotoTicket) { t.URL = "https://kage.talk.kakao.com/download/a" }, "filename header": func(t *PhotoTicket) { t.Resource = "synthetic/bad%0D%0Aname.jpg" }, "resource URL": func(t *PhotoTicket) { t.Resource = "https://example.com/file.jpg" }, "ready": func(t *PhotoTicket) { t.NeedReady = true }, "other strategy": func(t *PhotoTicket) { t.ResourceType = "vt" }} {
		t.Run(name, func(t *testing.T) {
			ticket := base
			change(&ticket)
			called := false
			err := UploadDirectPhoto(context.Background(), directDoerFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("unexpected call") }), directTestProfile(), ticket, Image{Data: []byte("synthetic"), Extension: "jpg"})
			if err == nil || called {
				t.Fatal("unsafe ticket reached transport")
			}
		})
	}
}

func TestDirectPhotoTransportErrorsDoNotExposeSignedURL(t *testing.T) {
	secret := "synthetic-sensitive-signed-url"
	ticket := PhotoTicket{URL: "https://kage.talk.kakao.com/offset-up/synthetic/ready/?credential=" + secret, Resource: "synthetic/photo.jpg", ResourceType: "vk", UploadKey: "synthetic0", ChunkSize: 4194304}
	err := UploadDirectPhoto(context.Background(), directDoerFunc(func(req *http.Request) (*http.Response, error) { return nil, errors.New(req.URL.String()) }), directTestProfile(), ticket, Image{Data: []byte("synthetic"), Extension: "jpg"})
	if !errors.Is(err, macweb.ErrTransport) || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

func TestDirectPhotoObservedChunkProgress(t *testing.T) {
	var fixture struct {
		Size      int
		ChunkSize int64 `json:"chunk_size"`
		Resource  string
		Requests  []struct{ Offset, Bytes int }
		Responses []json.RawMessage
	}
	raw, err := os.ReadFile("../../../research/fixtures/photo/observed-kage-chunks.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, fixture.Size)
	for i := range data {
		data[i] = byte(i >> 16)
	}
	ticket := PhotoTicket{URL: "https://kage.talk.kakao.com/offset-up/synthetic/ready/", Resource: fixture.Resource, ResourceType: "vk", UploadKey: "synthetic0", ChunkSize: fixture.ChunkSize}
	calls := 0
	doer := directDoerFunc(func(req *http.Request) (*http.Response, error) {
		if calls >= len(fixture.Requests) {
			t.Fatal("unexpected extra chunk")
		}
		want := fixture.Requests[calls]
		mr, err := req.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		part, err := mr.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		offset, _ := io.ReadAll(part)
		if string(offset) != strconv.Itoa(want.Offset) {
			t.Fatal("chunk starting offset differs")
		}
		part, err = mr.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		chunk, _ := io.ReadAll(part)
		if !bytes.Equal(chunk, data[want.Offset:want.Offset+want.Bytes]) {
			t.Fatal("chunk bytes overlap or skip")
		}
		resp := fixture.Responses[calls]
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(resp))}, nil
	})
	if err := UploadDirectPhoto(context.Background(), doer, directTestProfile(), ticket, Image{Data: data, Extension: "png"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("chunk calls %d", calls)
	}
}

func TestDirectPhotoInvalidAcknowledgementStopsRemainingChunks(t *testing.T) {
	ticket := PhotoTicket{URL: "https://kage.talk.kakao.com/offset-up/synthetic/ready/", Resource: "synthetic/photo.png", ResourceType: "vk", UploadKey: "synthetic0", ChunkSize: 4}
	calls := 0
	err := UploadDirectPhoto(context.Background(), directDoerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"offset":"1","msg":"ok"}`))}, nil
	}), directTestProfile(), ticket, Image{Data: []byte("synthetic bytes"), Extension: "png"})
	if !errors.Is(err, ErrInvalidOffset) || calls != 1 {
		t.Fatalf("acknowledgement %v; calls %d", err, calls)
	}
}
