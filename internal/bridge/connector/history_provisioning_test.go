package connector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/frrad/mooo/internal/continuity"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type cachedProvisionSource struct {
	*fakeKakao
	sdk          *client.Client
	reads        int
	acquisitions int
}

func (s *cachedProvisionSource) ReadStoredHistory(ctx context.Context, r client.StoredHistoryRequest) (client.StoredHistoryPage, error) {
	s.reads++
	return s.sdk.ReadStoredHistory(ctx, r)
}
func (s *cachedProvisionSource) ReadHistoryPage(context.Context, int64, int64, int64, int) (client.HistoryPage, error) {
	s.acquisitions++
	return client.HistoryPage{}, errors.New("unexpected source acquisition")
}

// This synthetic fixture represents a previously validated source page. Opening
// and reading it uses production storage code; it is not official parity evidence.
func provisioningCacheFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profile")
	store, err := authstate.Create(path, authstate.Config{DeviceName: "synthetic", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InstallCredentials(authstate.Credentials{UserID: testSelfID, AccessToken: "synthetic", AutoLoginMaterial: []byte("synthetic")}); err != nil {
		t.Fatal(err)
	}
	sdk, err := client.OpenWithOptions(path, nil, client.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = sdk.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := path + ".history-store"
	header, found, err := privatejson.Read[struct {
		Version uint32
		Binding string
	}](filepath.Join(dir, "header.json"))
	if err != nil || !found {
		t.Fatal(found, err)
	}
	request := syncmsg.Request{ChatID: testChatID, Cur: 0, Max: 500}
	var logs []bson.Raw
	for _, n := range []int64{100, 200, 300, 400, 500} {
		raw, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: n}, {Key: "authorId", Value: testOtherID}, {Key: "sendAt", Value: int64(1000)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: fmt.Sprintf("synthetic %d", n)}})
		if err != nil {
			t.Fatal(err)
		}
		logs = append(logs, raw)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d/0/500", testChatID)))
	page := struct {
		Version  uint32
		Binding  string
		Request  syncmsg.Request
		Response syncmsg.Response
	}{1, header.Binding, request, syncmsg.Response{ChatLogs: logs}}
	if err = privatejson.WriteInitial(filepath.Join(dir, "page-"+hex.EncodeToString(digest[:])+".json"), page); err != nil {
		t.Fatal(err)
	}
	return path
}

type provisionMatrixSink struct {
	mu           sync.Mutex
	lost         bool
	batches      int
	events       map[id.EventID]*event.Event
	sequences    [][]id.EventID
	readRequests int
}

func (s *provisionMatrixSink) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := any(map[string]any{})
	if strings.Contains(r.URL.Path, "/receipt/") || strings.HasSuffix(r.URL.Path, "/read_markers") {
		s.readRequests++
	}
	if strings.HasSuffix(r.URL.Path, "/batch_send") {
		var req mautrix.ReqBeeperBatchSend
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		s.batches++
		if req.Forward || req.SendNotification || req.MarkReadBy != "" {
			return nil, errors.New("backward batch changed notifications/read effects")
		}
		var sequence []id.EventID
		response := &mautrix.RespBeeperBatchSend{}
		for _, evt := range req.Events {
			if previous := s.events[evt.ID]; previous != nil && !reflect.DeepEqual(previous, evt) {
				return nil, errors.New("retry changed historical event")
			}
			s.events[evt.ID] = evt
			sequence = append(sequence, evt.ID)
			response.EventIDs = append(response.EventIDs, evt.ID)
		}
		s.sequences = append(s.sequences, sequence)
		if s.lost {
			s.lost = false
			return nil, io.ErrUnexpectedEOF
		}
		result = response
	} else if strings.HasSuffix(r.URL.Path, "/register") {
		result = map[string]any{"user_id": "@kakao_2000:test"}
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
}

func TestAuthenticatedHistoryHookActualCacheAndLostBatchRestart(t *testing.T) {
	path := provisioningCacheFixture(t)
	dbPath := filepath.Join(t.TempDir(), "bridge.db")
	sink := &provisionMatrixSink{lost: true, events: map[id.EventID]*event.Event{}}
	for attempt := range 2 {
		cfg := &bridgeconfig.Config{}
		cfg.Homeserver.Address = "http://synthetic.invalid"
		cfg.Homeserver.Domain = "test"
		cfg.AppService.UsernameTemplate = "kakao_{{.}}"
		cfg.AppService.Bot.Username = "bot"
		cfg.Provisioning.SharedSecret = "synthetic-history-hook-only"
		cfg.Bridge.Permissions = bridgeconfig.PermissionConfig{"@owner:test": &bridgeconfig.PermissionLevelAdmin}
		cfg.Backfill.Enabled = true
		cfg.Backfill.Queue.Manual = true
		cfg.Backfill.Queue.BatchSize = 2
		cfg.Backfill.Queue.MaxBatches = 10
		mx := matrix.NewConnector(cfg)
		network := &KakaoConnector{Config: Config{StartupHistory: true, ProfileDir: filepath.Dir(path)}}
		br, raw := newFrameworkBridge(t, frameworkOpts{path: dbPath, matrix: mx, network: network, config: &cfg.Bridge, commands: commands.NewProcessor})
		br.Config.Backfill = cfg.Backfill
		br.BackgroundCtx = t.Context()
		mx.Capabilities.BatchSending = true
		mx.AS.HTTPClient.Transport = sink
		mx.Bot.DefaultHTTPRetries = 0
		if err := mx.StateStore.Upgrade(t.Context()); err != nil {
			t.Fatal(err)
		}
		user, err := br.GetUserByMXID(t.Context(), "@owner:test")
		if err != nil {
			t.Fatal(err)
		}
		record := &database.UserLogin{ID: makeUserLoginID(testSelfID), BridgeID: br.ID, UserMXID: user.MXID, Metadata: &UserLoginMetadata{Profile: "profile"}}
		if attempt == 0 {
			if err = br.DB.UserLogin.Insert(t.Context(), record); err != nil {
				t.Fatal(err)
			}
		}
		login := &bridgev2.UserLogin{UserLogin: record, Bridge: br, User: user, Log: zerolog.Nop()}
		if err = network.LoadUserLogin(t.Context(), login); err != nil {
			t.Fatal(err)
		}
		kc := login.Client.(*KakaoClient)
		sdk, err := client.OpenWithOptions(path, nil, client.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := sdk.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		})
		source := &cachedProvisionSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 500}}}, sdk: sdk}
		kc.client = source
		// Let the real provisioning login lookup retrieve this live instance.
		// LoadUserLogin creates a stopped client, so install after framework lookup.
		loaded, err := br.GetExistingUserLoginByID(t.Context(), login.ID)
		if err != nil {
			t.Fatal(err)
		}
		loaded.Client = kc
		kc.login = loaded
		portal, err := br.GetPortalByKey(t.Context(), makePortalKey(testChatID, login.ID))
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			anchor := &database.Message{ID: makeMessageID(testChatID, 500), Room: portal.PortalKey, MXID: "$anchor:test", Timestamp: kakaoTime(1000), SenderID: makeUserID(testOtherID), SenderMXID: "@kakao_2000:test", Metadata: newKakaoMessageMetadata(testChatID, 500, testOtherID, 1, "synthetic 500", 0)}
			if err = br.DB.Message.Insert(t.Context(), anchor); err != nil {
				t.Fatal(err)
			}
			if err = br.DB.BackfillTask.EnsureExists(t.Context(), portal.PortalKey, login.ID); err != nil {
				t.Fatal(err)
			}
		}
		mx.Provisioning.Init()
		hit := func(auth bool) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/_matrix/provision/v3/backfill/!room:test?user_id=@owner:test", nil)
			if auth {
				req.Header.Set("Authorization", "Bearer synthetic-history-hook-only")
			}
			response := httptest.NewRecorder()
			mx.AS.Router.ServeHTTP(response, req)
			return response
		}
		if response := hit(false); response.Code != 401 || source.reads != 0 {
			t.Fatal("unauthorized acquisition", response.Code, source.reads)
		}
		if attempt == 0 {
			mx.Capabilities.BatchSending = false
			if response := hit(true); response.Code != 404 || source.reads != 0 {
				t.Fatal("unsupported insertion acquired history", response.Code, source.reads)
			}
			mx.Capabilities.BatchSending = true
			kc.client = nil
			if response := hit(true); response.Code != 403 || source.reads != 0 {
				t.Fatal("stopped login acquired history", response.Code, source.reads)
			}
			kc.client = source
		}
		response := hit(true)
		want := 200
		if attempt == 0 {
			want = 500
		}
		if response.Code != want {
			t.Fatal("hook result", attempt, response.Code, response.Body.String())
		}
		if source.acquisitions != 0 || source.reads != 1 {
			t.Fatal("cached route source accounting", source.reads, source.acquisitions)
		}
		if attempt == 0 {
			parts, err := kc.store.messageParts(t.Context(), login.ID, makeMessageID(testChatID, 300))
			if err != nil || len(parts) != 0 {
				t.Fatal("lost response confirmed a mapping", parts, err)
			}
			pending, found, err := kc.loadHistoryBatch(t.Context(), "kakao:history-batch:1000:3000:false")
			if err != nil || !found || !pending.Pending {
				t.Fatal("lost response forgot pending window", pending, found, err)
			}
		}
		if attempt == 1 {
			for _, n := range []int64{300, 400} {
				parts, err := kc.store.messageParts(t.Context(), login.ID, makeMessageID(testChatID, n))
				if err != nil || len(parts) != 1 {
					t.Fatal("missing mapping", n, parts, err)
				}
				if parts[0].MXID != mx.GenerateDeterministicEventID(portal.MXID, portal.PortalKey, makeMessageID(testChatID, n), "") {
					t.Fatal("persisted identity differs from HTTP batch", n, parts[0].MXID)
				}
			}
			if response = hit(true); response.Code != 200 || source.reads != 2 {
				t.Fatal("older page failed", response.Code, source.reads, response.Body.String())
			}
			for _, n := range []int64{100, 200} {
				parts, err := kc.store.messageParts(t.Context(), login.ID, makeMessageID(testChatID, n))
				if err != nil || len(parts) != 1 {
					t.Fatal("older page mapping", n, parts, err)
				}
			}
			if response = hit(true); response.Code != 204 || source.reads != 2 {
				t.Fatal("exhausted route fetched again", response.Code, source.reads)
			}
		}
		if len(source.marks) != 0 || len(source.commits) != 0 {
			t.Fatal("history became source read/live delivery", source.marks, source.commits)
		}
		if err = sdk.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		checkpoint, found, err := privatejson.Read[continuity.Checkpoint](path + ".continuity")
		if err != nil || !found || len(checkpoint.ReadWatermarks) != 0 || checkpoint.CommittedMax(testChatID) != 0 {
			t.Fatal("history changed read/live cursor", checkpoint, found, err)
		}
		loaded.BridgeState.Destroy()
		if err = raw.RawDB.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if sink.batches != 3 || len(sink.events) != 4 {
		t.Fatal("restart duplicates", sink.batches, len(sink.events))
	}
	if sink.readRequests != 0 || len(sink.sequences) != 3 || !reflect.DeepEqual(sink.sequences[0], sink.sequences[1]) {
		t.Fatal("retry order/read effects", sink.sequences, sink.readRequests)
	}
	var ordered []string
	for _, identity := range sink.sequences[0] {
		body, _ := sink.events[identity].Content.Raw["body"].(string)
		ordered = append(ordered, body)
	}
	if !reflect.DeepEqual(ordered, []string{"synthetic 300", "synthetic 400"}) {
		t.Fatal("history HTTP order", ordered)
	}
	var older []string
	for _, identity := range sink.sequences[2] {
		body, _ := sink.events[identity].Content.Raw["body"].(string)
		older = append(older, body)
	}
	if !reflect.DeepEqual(older, []string{"synthetic 100", "synthetic 200"}) {
		t.Fatal("older page order", older)
	}
	bodies := map[string]bool{}
	for _, evt := range sink.events {
		body, _ := evt.Content.Raw["body"].(string)
		bodies[body] = true
		if evt.Timestamp != 1000000 || evt.Sender != "@kakao_2000:test" {
			t.Fatal("historical identity/timestamp", evt.ID, evt.Timestamp, evt.Sender)
		}
	}
	if !reflect.DeepEqual(bodies, map[string]bool{"synthetic 100": true, "synthetic 200": true, "synthetic 300": true, "synthetic 400": true}) {
		t.Fatal("history body changed", bodies)
	}
}
