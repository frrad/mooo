package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/booking"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func reusableTestState() authstate.State {
	return authstate.State{
		Identity: authstate.Identity{
			DeviceUUID: "123e4567-e89b-42d3-a456-426614174000",
			Metadata:   authstate.MacMetadata{AppVersion: "26.8.0", OSVersion: "26.6.2"},
		},
		Credentials: &authstate.Credentials{UserID: 1, AccessToken: "token"},
	}
}

func TestClientConnectRenewsOnceOnExpiredToken(t *testing.T) {
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.json")
	store, err := authstate.Create(path, authstate.Config{
		DeviceName: "test", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	old := authstate.Credentials{UserID: 1, AccessToken: "old-access", AutoLoginMaterial: []byte(`{"refresh_token":"old-refresh","token_type":"bearer","display_account_id":"keep"}`)}
	if err := store.InstallCredentials(old); err != nil {
		t.Fatal(err)
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	doer := friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Header.Get("Authorization") == "" {
			t.Fatal("renewal omitted Authorization")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"rotated"}`,
		))}, nil
	})
	client, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	client.store = store
	dials := 0
	client.dial = func(_ context.Context, got authstate.State) (*session, error) {
		dials++
		if dials == 1 {
			return nil, StatusError{Command: "LOGINLIST", Status: -950}
		}
		if got.Credentials.AccessToken != "new-access" {
			t.Fatal("second login did not use rotated access token")
		}
		return &session{}, nil
	}
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || dials != 2 {
		t.Fatalf("requests=%d dials=%d, want 1 and 2", requests, dials)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != "new-access" || !strings.Contains(string(persisted.Credentials.AutoLoginMaterial), `"display_account_id":"keep"`) || !strings.Contains(string(persisted.Credentials.AutoLoginMaterial), `"refresh_token":"new-refresh"`) {
		t.Fatalf("rotated state did not preserve metadata: %v", persisted.Credentials)
	}
}

// renewalTestClient is a Client over a fresh profile whose renewal requests
// answer with body and whose dials return the next of results.
func renewalTestClient(t *testing.T, body string, results ...error) (*Client, *authstate.Store, *int, *int) {
	t.Helper()
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.json")
	store, err := authstate.Create(path, authstate.Config{
		DeviceName: "test", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	old := authstate.Credentials{UserID: 1, AccessToken: "old-access", AutoLoginMaterial: []byte(`{"refresh_token":"old-refresh","token_type":"bearer"}`)}
	if err := store.InstallCredentials(old); err != nil {
		t.Fatal(err)
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	requests, dials := new(int), new(int)
	doer := friendDoerFunc(func(*http.Request) (*http.Response, error) {
		*requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	client, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	client.store = store
	client.dial = func(context.Context, authstate.State) (*session, error) {
		*dials++
		if *dials > len(results) || results[*dials-1] == nil {
			return &session{}, nil
		}
		return nil, results[*dials-1]
	}
	return client, store, requests, dials
}

// A failed renewal leaves the stored credentials alone and fails the attempt
// as retryable; like the official client, the next attempt renews again.
func TestClientConnectFailedRenewalRetriesNextAttempt(t *testing.T) {
	expired := StatusError{Command: "LOGINLIST", Status: -950}
	client, store, requests, _ := renewalTestClient(t, `{"access_token":"incomplete"}`, expired, expired)
	before, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(t.Context()); !errors.Is(err, ErrCredentialRenewal) || errors.Is(err, ErrSignedOut) {
		t.Fatalf("first Connect error = %v, want a retryable ErrCredentialRenewal", err)
	}
	if err := client.Connect(t.Context()); !errors.Is(err, ErrCredentialRenewal) {
		t.Fatalf("second Connect error = %v, want ErrCredentialRenewal", err)
	}
	if *requests != 2 {
		t.Fatalf("renewal request count = %d, want one per attempt (2)", *requests)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != before.Credentials.AccessToken || string(persisted.Credentials.AutoLoginMaterial) != string(before.Credentials.AutoLoginMaterial) {
		t.Fatal("failed renewal modified persisted credentials")
	}
}

// The official client renews on every -950 and resends LOGINLIST at once,
// with no renewal limit (RECOVERY-TRACE RT-STATIC-002).
func TestClientConnectRenewsOnEveryExpiredToken(t *testing.T) {
	expired := StatusError{Command: "LOGINLIST", Status: -950}
	client, _, requests, dials := renewalTestClient(t, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"bearer"}`, expired, expired, expired, nil)
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if *requests != 3 || *dials != 4 {
		t.Fatalf("renewals=%d dials=%d, want 3 and 4", *requests, *dials)
	}
}

// A renewal that KakaoTalk ends with -998 or -999 is the native logout.
func TestClientConnectRenewalSignOutIsTerminal(t *testing.T) {
	for _, status := range []string{"-998", "-999"} {
		client, _, requests, dials := renewalTestClient(t, `{"status":`+status+`}`, StatusError{Command: "LOGINLIST", Status: -950})
		if err := client.Connect(t.Context()); !errors.Is(err, ErrSignedOut) {
			t.Fatalf("renewal status %s: Connect error = %v, want ErrSignedOut", status, err)
		}
		if *requests != 1 || *dials != 1 {
			t.Fatalf("renewal status %s: renewals=%d dials=%d, want 1 and 1", status, *requests, *dials)
		}
	}
}

func setPrivateDir(path string) error {
	return os.Chmod(path, 0o700)
}

func TestClientConnectReusesOneSession(t *testing.T) {
	client, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	connects := 0
	client.dial = func(context.Context, authstate.State) (*session, error) {
		connects++
		return &session{}, nil
	}
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if connects != 1 {
		t.Fatalf("dial count = %d, want 1", connects)
	}
}

func TestClientShutdownPreventsReconnect(t *testing.T) {
	client, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(t.Context()); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Connect after Shutdown = %v, want ErrClientClosed", err)
	}
}

func TestScriptedBackendLoginTextPushAndPhoto(t *testing.T) {
	const (
		userID = int64(7)
		chatID = int64(42)
	)
	firstChat := mustBSON(bson.D{{Key: "c", Value: chatID}})
	secondChat := mustBSON(bson.D{{Key: "c", Value: int64(43)}})
	bookingBackend := newScriptedBackend(t, false, testloco.Expect("GETCONF", func(raw bson.Raw) error {
		if err := requireInt64(raw, "userId", userID); err != nil {
			return err
		}
		return requireString(raw, "os", "mac")
	}, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, testloco.Expect("CHECKIN", func(raw bson.Raw) error {
		if err := requireInt64(raw, "userId", userID); err != nil {
			return err
		}
		return requireString(raw, "appVer", "26.8.0")
	}, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))

	pushTrigger := make(chan struct{})
	carriage := newScriptedBackend(t, true,
		testloco.Expect("LOGINLIST", func(raw bson.Raw) error {
			if err := requireString(raw, "oauthToken", "test-access"); err != nil {
				return err
			}
			return requireString(raw, "duuid", "ATy7owKUe75Z+WlbvLq8nxZUhjYyAVnr4yGREkhLqqCqEWTdk7JemPShx77n/WArwqYUwA==")
		}, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{bson.Raw(firstChat)}},
			bson.E{Key: "eof", Value: false}, bson.E{Key: "lastTokenId", Value: int64(11)}, bson.E{Key: "lastChatId", Value: chatID},
		)),
		testloco.Expect("LCHATLIST", func(raw bson.Raw) error {
			if err := requireInt64(raw, "lastTokenId", 11); err != nil {
				return err
			}
			return requireInt64(raw, "lastChatId", chatID)
		}, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{bson.Raw(secondChat)}}, bson.E{Key: "eof", Value: true},
		)),
		testloco.PushAfter(pushTrigger, "MSG", bson.D{{Key: "chatId", Value: chatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic inbound"}}}}),
		testloco.Expect("WRITE", func(raw bson.Raw) error {
			if err := requireExactKeys(raw, "chatId", "msg", "type", "noSeen"); err != nil {
				return err
			}
			if err := requireInt64(raw, "chatId", chatID); err != nil {
				return err
			}
			return requireString(raw, "msg", "synthetic outbound")
		}, statusDocument(bson.E{Key: "chatId", Value: chatID}, bson.E{Key: "logId", Value: int64(101)})),
		testloco.Expect("WRITE", func(raw bson.Raw) error {
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
			if err != nil || typeValue.Type != bson.TypeInt32 || typeValue.Int32() != messagetype.Reply {
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
			if attachment.LogID != 100 || attachment.UserID != 8 || attachment.Type != messagetype.Text || attachment.Message != "synthetic inbound" {
				return fmt.Errorf("reply attachment = %#v", attachment)
			}
			return nil
		}, statusDocument(bson.E{Key: "chatId", Value: chatID}, bson.E{Key: "logId", Value: int64(102)})),
		testloco.Expect("CREATE", func(raw bson.Raw) error {
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
		testloco.Expect("SHIP", func(raw bson.Raw) error {
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
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
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
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument(bson.E{Key: "o", Value: int64(1)})); err != nil {
			return err
		}
		payload, err := server.ReadPayload()
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, imageData[1:]) {
			return errors.New("uploaded bytes did not honor POST offset")
		}
		return server.WriteBSON(0, "COMPLETE", statusDocument(bson.E{Key: "chatLog", Value: bson.Raw(completeLog)}))
	})

	var dialMu sync.Mutex
	tlsDials, secureDials := 0, 0
	counted := func(count *int, hosts testloco.Hosts) TestDialer {
		return func(ctx context.Context, host string, port int) (net.Conn, *loco.SecureV3, error) {
			dialMu.Lock()
			defer dialMu.Unlock()
			*count++
			return hosts.Dial(ctx, host, port)
		}
	}
	dialers := testSessionDialers(TestDialers{
		TLS:    counted(&tlsDials, testloco.Hosts{booking.Host: bookingBackend, "checkin.invalid": checkin}),
		Secure: counted(&secureDials, testloco.Hosts{"carriage.invalid": carriage, "media.invalid": mediaBackend}),
	})
	state := reusableTestState()
	state.Credentials.UserID = userID
	state.Credentials.AccessToken = "test-access"
	state.Identity.Metadata.DeviceModel = "synthetic-model"
	api, err := newClient(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	api.dial = func(ctx context.Context, got authstate.State) (*session, error) {
		return connectSessionWithDialers(ctx, got, dialers)
	}
	defer func() { _ = api.Shutdown(t.Context()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	chats, err := api.initialChatData(ctx)
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
		Target: chat.ReplyTarget{LogID: 100, UserID: 8, Type: messagetype.Text, Message: "synthetic inbound"},
	})
	if err != nil || reply.ChatID != chatID || reply.LogID != 102 {
		t.Fatalf("reply WRITE response = %#v, err=%v", reply, err)
	}
	created, err := api.CreateChat(ctx, chat.CreateRequest{MemberIDs: []int64{99}})
	if err != nil || created.ChatID != 44 || created.ChatRoom.Lookup("type").StringValue() != "DirectChat" {
		t.Fatalf("CREATE response = %#v, err=%v", created, err)
	}
	photo, err := api.SendImage(ctx, chatID, imageData, "")
	if err != nil || photo.ChatLog.Lookup("logId").Int64() != 103 {
		t.Fatalf("photo result log=%v, err=%v", photo.ChatLog, err)
	}

	waitBackends(t, bookingBackend, checkin, carriage, mediaBackend)
	dialMu.Lock()
	defer dialMu.Unlock()
	if tlsDials != 2 || secureDials != 2 {
		t.Fatalf("TLS dials=%d secure dials=%d, want 2 and 2", tlsDials, secureDials)
	}
}

func TestScriptedBackendAmbiguousWriteIsNeverRetried(t *testing.T) {
	requests := 0
	scripted, backend := scriptedSession(t, testloco.DropAfter("WRITE", &requests))
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	api.dial = func(context.Context, authstate.State) (*session, error) {
		dials++
		return scripted, nil
	}
	defer func() { _ = api.Shutdown(t.Context()) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := api.SendText(ctx, 42, "one attempt only"); err == nil {
		t.Fatal("ambiguous WRITE unexpectedly succeeded")
	}
	if _, err := api.SendText(ctx, 42, "must not reconnect"); !errors.Is(err, ErrClosed) {
		t.Fatalf("second WRITE error = %v, want ErrClosed", err)
	}
	waitBackends(t, backend)
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
	api.dial = func(ctx context.Context, got authstate.State) (*session, error) {
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
	defer func() { _ = api.Shutdown(t.Context()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := api.Connect(ctx); err != nil {
		t.Fatal(err)
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
	_ = api.Shutdown(ctx)
	waitBackends(t, append(firstBackends, secondBackends...)...)
}
