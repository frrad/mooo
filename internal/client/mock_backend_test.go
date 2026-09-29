package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type backendStep func(*wireConn) error

type scriptedBackend struct {
	client *wireConn
	done   chan error
}

func newScriptedBackend(t *testing.T, secure bool, steps ...backendStep) *scriptedBackend {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	clientWire := &wireConn{c: clientConn}
	serverWire := &wireConn{c: serverConn}
	if secure {
		clientSecure, err := loco.NewSecureV3(nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		serverSecure, err := loco.NewSecureV3WithKey(clientSecure.KeyForTesting(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		clientWire.secure = clientSecure
		serverWire.secure = serverSecure
	}
	backend := &scriptedBackend{client: clientWire, done: make(chan error, 1)}
	go func() {
		defer func() { _ = serverWire.close() }()
		for index, step := range steps {
			if err := step(serverWire); err != nil {
				backend.done <- fmt.Errorf("script step %d: %w", index+1, err)
				return
			}
		}
		backend.done <- nil
	}()
	return backend
}

func (b *scriptedBackend) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-b.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scripted backend did not finish")
	}
}

func expectRequest(method string, check func(bson.Raw) error, reply bson.D) backendStep {
	return func(server *wireConn) error {
		request, err := server.read()
		if err != nil {
			return err
		}
		if request.Header.Method != method {
			return fmt.Errorf("method = %q, want %q", request.Header.Method, method)
		}
		body := bson.Raw(request.Body)
		if err := body.Validate(); err != nil {
			return fmt.Errorf("invalid request BSON: %w", err)
		}
		if check != nil {
			if err := check(body); err != nil {
				return err
			}
		}
		return writeBackendPacket(server, request.Header.PacketID, method, mustBSON(reply))
	}
}

func pushAfter(trigger <-chan struct{}, method string, body bson.D) backendStep {
	return func(server *wireConn) error {
		select {
		case <-trigger:
		case <-time.After(2 * time.Second):
			return errors.New("push trigger timeout")
		}
		return writeBackendPacket(server, 0, method, mustBSON(body))
	}
}

func disconnectAfterRequest(method string, count *int) backendStep {
	return func(server *wireConn) error {
		request, err := server.read()
		if err != nil {
			return err
		}
		if request.Header.Method != method {
			return fmt.Errorf("method = %q, want %q", request.Header.Method, method)
		}
		(*count)++
		return server.close()
	}
}

func writeBackendPacket(server *wireConn, id uint32, method string, body []byte) error {
	raw, err := (loco.Packet{Header: loco.Header{PacketID: id, Method: method, BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		return err
	}
	if server.secure != nil {
		raw, err = server.secure.Encrypt(raw)
		if err != nil {
			return err
		}
	}
	return writeAll(server.c, raw)
}

func readSecurePayload(server *wireConn) ([]byte, error) {
	prefix := make([]byte, 4)
	if _, err := io.ReadFull(server.c, prefix); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(prefix)
	if size > loco.DefaultMaxCiphertext {
		return nil, loco.ErrCiphertextTooLarge
	}
	envelope := make([]byte, 4+int(size))
	copy(envelope, prefix)
	if _, err := io.ReadFull(server.c, envelope[4:]); err != nil {
		return nil, err
	}
	return server.secure.Decrypt(envelope)
}

func mustBSON(document bson.D) []byte {
	body, err := bson.Marshal(document)
	if err != nil {
		panic(err)
	}
	return body
}

func statusDocument(fields ...bson.E) bson.D {
	return append(bson.D{{Key: "status", Value: int32(0)}}, fields...)
}

func requireInt64(raw bson.Raw, key string, want int64) error {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeInt64 || value.Int64() != want {
		return fmt.Errorf("%s is not int64(%d)", key, want)
	}
	return nil
}

func requireString(raw bson.Raw, key, want string) error {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeString || value.StringValue() != want {
		return fmt.Errorf("%s is not %q", key, want)
	}
	return nil
}

func requireExactKeys(raw bson.Raw, want ...string) error {
	elements, err := raw.Elements()
	if err != nil {
		return err
	}
	if len(elements) != len(want) {
		return fmt.Errorf("key count = %d, want %d", len(elements), len(want))
	}
	for index, element := range elements {
		if element.Key() != want[index] {
			return fmt.Errorf("key %d = %q, want %q", index, element.Key(), want[index])
		}
	}
	return nil
}

func syntheticClientJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 0xff, G: 0x40, A: 0xff})
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func scriptedLoginAttempt(t *testing.T, expectedToken string, loginReply bson.D) (sessionDialers, []*scriptedBackend) {
	t.Helper()
	booking := newScriptedBackend(t, false, expectRequest("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, expectRequest("CHECKIN", nil, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))
	carriage := newScriptedBackend(t, true, expectRequest("LOGINLIST", func(raw bson.Raw) error {
		return requireString(raw, "oauthToken", expectedToken)
	}, loginReply))
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			switch host {
			case bookingHost:
				return booking.client, nil
			case "checkin.invalid":
				return checkin.client, nil
			default:
				return nil, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		secure: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host != "carriage.invalid" {
				return nil, fmt.Errorf("unexpected secure host %q", host)
			}
			return carriage.client, nil
		},
	}
	return dialers, []*scriptedBackend{booking, checkin, carriage}
}

func TestScriptedBackendLoginTextPushAndPhoto(t *testing.T) {
	const (
		userID = int64(7)
		chatID = int64(42)
	)
	firstChat := mustBSON(bson.D{{Key: "c", Value: chatID}})
	secondChat := mustBSON(bson.D{{Key: "c", Value: int64(43)}})
	booking := newScriptedBackend(t, false, expectRequest("GETCONF", func(raw bson.Raw) error {
		if err := requireInt64(raw, "userId", userID); err != nil {
			return err
		}
		return requireString(raw, "os", "mac")
	}, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, expectRequest("CHECKIN", func(raw bson.Raw) error {
		if err := requireInt64(raw, "userId", userID); err != nil {
			return err
		}
		return requireString(raw, "appVer", "26.8.0")
	}, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))

	pushTrigger := make(chan struct{})
	carriage := newScriptedBackend(t, true,
		expectRequest("LOGINLIST", func(raw bson.Raw) error {
			if err := requireString(raw, "oauthToken", "test-access"); err != nil {
				return err
			}
			return requireString(raw, "duuid", "ATy7owKUe75Z+WlbvLq8nxZUhjYyAVnr4yGREkhLqqCqEWTdk7JemPShx77n/WArwqYUwA==")
		}, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{bson.Raw(firstChat)}},
			bson.E{Key: "eof", Value: false}, bson.E{Key: "lastTokenId", Value: int64(11)}, bson.E{Key: "lastChatId", Value: chatID},
		)),
		expectRequest("LCHATLIST", func(raw bson.Raw) error {
			if err := requireInt64(raw, "lastTokenId", 11); err != nil {
				return err
			}
			return requireInt64(raw, "lastChatId", chatID)
		}, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{bson.Raw(secondChat)}}, bson.E{Key: "eof", Value: true},
		)),
		pushAfter(pushTrigger, "MSG", bson.D{{Key: "chatId", Value: chatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic inbound"}}}}),
		expectRequest("WRITE", func(raw bson.Raw) error {
			if err := requireExactKeys(raw, "chatId", "msg", "type", "noSeen"); err != nil {
				return err
			}
			if err := requireInt64(raw, "chatId", chatID); err != nil {
				return err
			}
			return requireString(raw, "msg", "synthetic outbound")
		}, statusDocument(bson.E{Key: "chatId", Value: chatID}, bson.E{Key: "logId", Value: int64(101)})),
		expectRequest("WRITE", func(raw bson.Raw) error {
			if err := requireExactKeys(raw, "chatId", "msg", "type", "noSeen", "extra"); err != nil {
				return err
			}
			if err := requireInt64(raw, "chatId", chatID); err != nil {
				return err
			}
			if err := requireString(raw, "msg", "synthetic reply"); err != nil {
				return err
			}
			typeValue, err := raw.LookupErr("type")
			if err != nil || typeValue.Type != bson.TypeInt32 || typeValue.Int32() != chat.ReplyType {
				return errors.New("reply type is not int32(26)")
			}
			extra, err := raw.LookupErr("extra")
			if err != nil || extra.Type != bson.TypeString {
				return errors.New("reply extra is not a string")
			}
			var attachment struct {
				LogID   int64  `json:"src_logId"`
				UserID  int64  `json:"src_userId"`
				Type    int32  `json:"src_type"`
				Message string `json:"src_message"`
			}
			if err := json.Unmarshal([]byte(extra.StringValue()), &attachment); err != nil {
				return err
			}
			if attachment.LogID != 100 || attachment.UserID != 8 || attachment.Type != chat.TextType || attachment.Message != "synthetic inbound" {
				return fmt.Errorf("reply attachment = %#v", attachment)
			}
			return nil
		}, statusDocument(bson.E{Key: "chatId", Value: chatID}, bson.E{Key: "logId", Value: int64(102)})),
		expectRequest("CREATE", func(raw bson.Raw) error {
			members, err := raw.LookupErr("memberIds")
			if err != nil || members.Type != bson.TypeArray {
				return errors.New("memberIds is not an array")
			}
			values, err := members.Array().Values()
			if err != nil || len(values) != 1 || values[0].Type != bson.TypeInt64 || values[0].Int64() != 99 {
				return errors.New("memberIds does not contain the synthetic peer")
			}
			return nil
		}, statusDocument(
			bson.E{Key: "chatId", Value: int64(44)},
			bson.E{Key: "chatRoom", Value: bson.D{{Key: "id", Value: int64(44)}, {Key: "type", Value: "DirectChat"}}},
		)),
		expectRequest("SHIP", func(raw bson.Raw) error {
			if err := requireInt64(raw, "c", chatID); err != nil {
				return err
			}
			return requireString(raw, "e", "jpg")
		}, statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		)),
	)

	imageData := syntheticClientJPEG(t)
	completeLog := mustBSON(bson.D{{Key: "type", Value: int32(2)}, {Key: "chatId", Value: chatID}, {Key: "logId", Value: int64(103)}})
	mediaBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.read()
		if err != nil {
			return err
		}
		if request.Header.Method != "POST" {
			return fmt.Errorf("method = %q, want POST", request.Header.Method)
		}
		raw := bson.Raw(request.Body)
		if err := requireString(raw, "k", "synthetic-ticket"); err != nil {
			return err
		}
		if err := requireInt64(raw, "c", chatID); err != nil {
			return err
		}
		if err := writeBackendPacket(server, request.Header.PacketID, "POST", mustBSON(statusDocument(bson.E{Key: "o", Value: int64(1)}))); err != nil {
			return err
		}
		payload, err := readSecurePayload(server)
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, imageData[1:]) {
			return errors.New("uploaded bytes did not honor POST offset")
		}
		return writeBackendPacket(server, 0, "COMPLETE", mustBSON(statusDocument(bson.E{Key: "chatLog", Value: bson.Raw(completeLog)})))
	})

	var dialMu sync.Mutex
	tlsDials, secureDials := 0, 0
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			dialMu.Lock()
			defer dialMu.Unlock()
			tlsDials++
			switch host {
			case bookingHost:
				return booking.client, nil
			case "checkin.invalid":
				return checkin.client, nil
			default:
				return nil, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		secure: func(_ context.Context, host string, _ int) (*wireConn, error) {
			dialMu.Lock()
			defer dialMu.Unlock()
			secureDials++
			switch host {
			case "carriage.invalid":
				return carriage.client, nil
			case "media.invalid":
				return mediaBackend.client, nil
			default:
				return nil, fmt.Errorf("unexpected secure host %q", host)
			}
		},
	}
	state := reusableTestState()
	state.Credentials.UserID = userID
	state.Credentials.AccessToken = "test-access"
	state.Identity.Metadata.DeviceModel = "synthetic-model"
	api, err := newClient(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	api.dial = func(ctx context.Context, got authstate.State) (*Session, error) {
		return connectSessionWithDialers(ctx, got, dialers)
	}
	defer func() { _ = api.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	chats, err := api.InitialChatData(ctx)
	if err != nil || len(chats) != 2 {
		t.Fatalf("initial chats = %d, err=%v", len(chats), err)
	}
	eventStream, err := api.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondEventStream, err := api.Events(ctx)
	if err != nil || secondEventStream != eventStream {
		t.Fatal("Events did not return the reusable typed stream")
	}
	if _, err := api.Pushes(ctx); !errors.Is(err, ErrPushConsumerSelected) {
		t.Fatalf("Pushes after Events error = %v", err)
	}
	close(pushTrigger)
	select {
	case result := <-eventStream:
		message, ok := result.Event.(events.TextMessage)
		if result.Err != nil || !ok || message.Message != "synthetic inbound" || message.LogID != 100 {
			t.Fatalf("typed event = %T %#v, err=%v", result.Event, result.Event, result.Err)
		}
	case <-ctx.Done():
		t.Fatal("idle MSG push was not delivered")
	}
	write, err := api.SendText(ctx, chatID, "synthetic outbound")
	if err != nil || write.ChatID != chatID || write.LogID != 101 {
		t.Fatalf("WRITE response = %#v, err=%v", write, err)
	}
	reply, err := api.SendReply(ctx, chat.ReplyRequest{
		ChatID: chatID, Message: "synthetic reply",
		Target: chat.ReplyTarget{LogID: 100, UserID: 8, Type: chat.TextType, Message: "synthetic inbound"},
	})
	if err != nil || reply.ChatID != chatID || reply.LogID != 102 {
		t.Fatalf("reply WRITE response = %#v, err=%v", reply, err)
	}
	created, err := api.CreateChat(ctx, chat.CreateRequest{MemberIDs: []int64{99}})
	if err != nil || created.ChatID != 44 || created.ChatRoom.Lookup("type").StringValue() != "DirectChat" {
		t.Fatalf("CREATE response = %#v, err=%v", created, err)
	}
	photo, err := api.SendImage(ctx, chatID, imageData)
	if err != nil || photo.ChatLog.Lookup("logId").Int64() != 103 {
		t.Fatalf("photo result log=%v, err=%v", photo.ChatLog, err)
	}

	booking.wait(t)
	checkin.wait(t)
	carriage.wait(t)
	mediaBackend.wait(t)
	dialMu.Lock()
	defer dialMu.Unlock()
	if tlsDials != 2 || secureDials != 2 {
		t.Fatalf("TLS dials=%d secure dials=%d, want 2 and 2", tlsDials, secureDials)
	}
}

func TestScriptedBackendAmbiguousWriteIsNeverRetried(t *testing.T) {
	requests := 0
	backend := newScriptedBackend(t, true, disconnectAfterRequest("WRITE", &requests))
	session := &Session{
		wire: backend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
	}
	go session.readLoop()
	state := reusableTestState()
	api, err := newClient(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		dials++
		return session, nil
	}
	defer func() { _ = api.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := api.SendText(ctx, 42, "one attempt only"); err == nil {
		t.Fatal("ambiguous WRITE unexpectedly succeeded")
	}
	if _, err := api.SendText(ctx, 42, "must not reconnect"); !errors.Is(err, ErrClosed) {
		t.Fatalf("second WRITE error = %v, want ErrClosed", err)
	}
	backend.wait(t)
	if requests != 1 || dials != 1 {
		t.Fatalf("requests=%d dials=%d, want exactly one each", requests, dials)
	}
}

func TestScriptedBackendExpiredTokenRenewsThenLogsInOnce(t *testing.T) {
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	store, err := authstate.Create(root+"/state.json", authstate.Config{
		DeviceName: "synthetic", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "synthetic-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCredentials(authstate.Credentials{
		UserID: 7, AccessToken: "expired-access",
		AutoLoginMaterial: []byte(`{"refresh_token":"old-refresh","token_type":"bearer"}`),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	firstDialers, firstBackends := scriptedLoginAttempt(t, "expired-access", bson.D{{Key: "status", Value: int32(-950)}})
	secondDialers, secondBackends := scriptedLoginAttempt(t, "rotated-access", statusDocument(
		bson.E{Key: "chatDatas", Value: bson.A{}}, bson.E{Key: "eof", Value: true},
	))
	renewals := 0
	doer := friendDoerFunc(func(request *http.Request) (*http.Response, error) {
		renewals++
		if request.URL.Path != "/mac/account/renew_token.json" {
			return nil, fmt.Errorf("unexpected HTTP path %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewBufferString(
				`{"access_token":"rotated-access","refresh_token":"rotated-refresh","token_type":"bearer"}`,
			)),
		}, nil
	})
	api, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	api.store = store
	loginAttempts := 0
	api.dial = func(ctx context.Context, got authstate.State) (*Session, error) {
		loginAttempts++
		switch loginAttempts {
		case 1:
			return connectSessionWithDialers(ctx, got, firstDialers)
		case 2:
			return connectSessionWithDialers(ctx, got, secondDialers)
		default:
			return nil, errors.New("unexpected third login attempt")
		}
	}
	defer func() { _ = api.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := api.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, backend := range append(firstBackends, secondBackends...) {
		backend.wait(t)
	}
	if renewals != 1 || loginAttempts != 2 {
		t.Fatalf("renewals=%d login attempts=%d, want 1 and 2", renewals, loginAttempts)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != "rotated-access" {
		t.Fatal("rotated access token was not persisted")
	}
	if err := api.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if renewals != 1 || loginAttempts != 2 {
		t.Fatal("repeated Connect renewed or logged in again")
	}
}

func TestScriptedBackendAmbiguousPhotoCompleteIsNeverRetried(t *testing.T) {
	imageData := syntheticClientJPEG(t)
	shipRequests := 0
	mainBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.read()
		if err != nil {
			return err
		}
		if request.Header.Method != "SHIP" {
			return fmt.Errorf("method = %q, want SHIP", request.Header.Method)
		}
		shipRequests++
		return writeBackendPacket(server, request.Header.PacketID, "SHIP", mustBSON(statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		)))
	})
	postRequests := 0
	mediaBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.read()
		if err != nil {
			return err
		}
		if request.Header.Method != "POST" {
			return fmt.Errorf("method = %q, want POST", request.Header.Method)
		}
		postRequests++
		if err := writeBackendPacket(server, request.Header.PacketID, "POST", mustBSON(statusDocument())); err != nil {
			return err
		}
		payload, err := readSecurePayload(server)
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, imageData) {
			return errors.New("uploaded photo bytes mismatch")
		}
		return server.close()
	})
	mediaDials := 0
	session := &Session{
		wire: mainBackend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
		mediaDial: func(_ context.Context, host string, _ int) (*wireConn, error) {
			mediaDials++
			if host != "media.invalid" {
				return nil, fmt.Errorf("unexpected media host %q", host)
			}
			return mediaBackend.client, nil
		},
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := session.SendImage(ctx, 42, imageData); err == nil {
		t.Fatal("photo without COMPLETE unexpectedly succeeded")
	}
	if _, err := session.SendImage(ctx, 42, imageData); !errors.Is(err, ErrClosed) {
		t.Fatalf("second photo error = %v, want ErrClosed", err)
	}
	mainBackend.wait(t)
	mediaBackend.wait(t)
	if shipRequests != 1 || postRequests != 1 || mediaDials != 1 {
		t.Fatalf("SHIP=%d POST=%d media dials=%d, want one each", shipRequests, postRequests, mediaDials)
	}
}
