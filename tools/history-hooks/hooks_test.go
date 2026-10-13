// Package historyhooks exercises the pinned bridge framework without any source
// account, real source-network implementation, or Matrix/Beeper server.
package historyhooks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type rejectOutbound struct{ calls atomic.Int32 }

func (r *rejectOutbound) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return nil, errors.New("history hook harness rejects outbound HTTP")
}

type source struct {
	bridgev2.NetworkAPI
	calls  atomic.Int32
	more   atomic.Bool
	params chan bridgev2.FetchMessagesParams
}

func (*source) IsLoggedIn() bool { return true }
func (s *source) FetchMessages(_ context.Context, p bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	s.calls.Add(1)
	s.params <- p
	return &bridgev2.FetchMessagesResponse{HasMore: s.more.Load(), Cursor: networkid.PaginationCursor("synthetic-cursor")}, nil
}

type network struct {
	bridgev2.NetworkConnector
	s *source
}

func (*network) Init(*bridgev2.Bridge)              {}
func (*network) GetDBMetaTypes() database.MetaTypes { return database.MetaTypes{} }
func (n *network) LoadUserLogin(_ context.Context, l *bridgev2.UserLogin) error {
	l.Client = n.s
	return nil
}

func TestPaginationHTTPHook(t *testing.T) {
	ctx := t.Context()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	cfg := &bridgeconfig.Config{}
	cfg.Homeserver.Address = "http://127.0.0.1:1"
	cfg.Homeserver.Domain = "test.invalid"
	cfg.AppService.UsernameTemplate = "hook_{{.}}"
	cfg.AppService.Bot.Username = "bot"
	cfg.Provisioning.SharedSecret = "synthetic-hook-auth-only"
	cfg.Bridge.Permissions = bridgeconfig.PermissionConfig{"@owner:test.invalid": &bridgeconfig.PermissionLevelAdmin}
	cfg.Backfill.Enabled = true
	cfg.Backfill.Queue.Manual = true
	cfg.Backfill.Queue.BatchSize = 5
	cfg.Backfill.Queue.BatchDelay = 3600
	cfg.Backfill.Queue.MaxBatches = 10
	mx := matrix.NewConnector(cfg)
	s := &source{params: make(chan bridgev2.FetchMessagesParams, 10)}
	s.more.Store(true)
	br := bridgev2.NewBridge("hooks", raw, zerolog.Nop(), &cfg.Bridge, mx, &network{s: s}, commands.NewProcessor)
	blocked := &rejectOutbound{}
	mx.AS.HTTPClient.Transport = blocked
	br.Config.Backfill = cfg.Backfill
	br.BackgroundCtx = ctx
	if err = br.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := br.GetUserByMXID(ctx, "@owner:test.invalid")
	if err != nil {
		t.Fatal(err)
	}
	login, err := user.NewLogin(ctx, &database.UserLogin{ID: "synthetic"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(login.BridgeState.Destroy)
	key := networkid.PortalKey{ID: "synthetic-room", Receiver: login.ID}
	portal, err := br.GetPortalByKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	portal.MXID = "!synthetic:test.invalid"
	if err = portal.Save(ctx); err != nil {
		t.Fatal(err)
	}
	mx.Provisioning.Init()
	hit := func(auth bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/_matrix/provision/v3/backfill/!synthetic:test.invalid?user_id=@owner:test.invalid", nil)
		if auth {
			r.Header.Set("Authorization", "Bearer synthetic-hook-auth-only")
		}
		w := httptest.NewRecorder()
		mx.AS.Router.ServeHTTP(w, r)
		return w
	}
	require := func(w *httptest.ResponseRecorder, code int, calls int32) {
		t.Helper()
		if w.Code != code || s.calls.Load() != calls {
			t.Fatalf("HTTP=%d calls=%d want HTTP=%d calls=%d body=%s", w.Code, s.calls.Load(), code, calls, w.Body.String())
		}
	}
	require(hit(false), http.StatusUnauthorized, 0)
	require(hit(true), http.StatusNotFound, 0) // capability gate
	mx.Capabilities.BatchSending = true
	cfg.Backfill.Queue.Manual = false
	require(hit(true), http.StatusNotFound, 0) // manual gate
	cfg.Backfill.Queue.Manual = true
	require(hit(true), http.StatusNoContent, 0) // no task
	if err = br.DB.BackfillTask.EnsureExists(ctx, key, login.ID); err != nil {
		t.Fatal(err)
	}
	task, err := br.DB.BackfillTask.GetNextForPortal(ctx, key, false)
	if err != nil {
		t.Fatal(err)
	}
	task.NextDispatchMinTS = time.Now().Add(time.Hour)
	if err = br.DB.BackfillTask.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	background, err := br.DB.BackfillTask.GetNext(ctx)
	if err != nil || background != nil {
		t.Fatalf("future task selected by background queue: %v %v", background, err)
	}
	require(hit(true), http.StatusOK, 1)
	require(hit(true), http.StatusOK, 2) // immediate repeated demand bypasses one-hour delay
	s.more.Store(false)
	require(hit(true), http.StatusOK, 3)
	require(hit(true), http.StatusNoContent, 3) // exhausted task does not call source again
	for i := 0; i < 3; i++ {
		p := <-s.params
		wantCursor := networkid.PaginationCursor("")
		if i > 0 {
			wantCursor = "synthetic-cursor"
		}
		if p.Portal.PortalKey != key || p.Forward || p.Count != 5 || !p.AllowSlowFetch || p.Cursor != wantCursor {
			t.Fatalf("unexpected callback parameters on request %d: %+v", i, p)
		}
	}
	if blocked.calls.Load() != 0 {
		t.Fatalf("unexpected outbound HTTP attempts: %d", blocked.calls.Load())
	}
	t.Log("authenticated HTTP route: gates, no task, three synthetic callbacks, delay bypass, and exhaustion verified")
}
