package connector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/shadow"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// shadowCheckedOptions opens scripted clients with the bridge's login policy
// and the BSON shadow in panic mode, so every synthetic body the backend sends
// must decode identically under both decoders.
func shadowCheckedOptions() client.OpenOptions {
	return client.OpenOptions{
		FullChatList: true,
		// Keep real admission enabled with short synthetic-test intervals.
		HistoryPacing:  client.HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: 10 * time.Millisecond},
		PacketObserver: shadow.NewObserver(shadow.Config{Mode: shadow.ModePanic}),
	}
}

type observedClient struct {
	kakaoClient
	mu          sync.Mutex
	eventsCalls int
	committed   []int64
}

func (c *observedClient) Events(ctx context.Context) (<-chan events.Result, error) {
	c.mu.Lock()
	c.eventsCalls++
	c.mu.Unlock()
	return c.kakaoClient.Events(ctx)
}

func (c *observedClient) CommitEvent(evt events.Event) error {
	if _, logID, ok := events.MessagePosition(evt); ok {
		c.mu.Lock()
		c.committed = append(c.committed, logID)
		c.mu.Unlock()
	}
	return c.kakaoClient.CommitEvent(evt)
}

func (c *observedClient) subscriptions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.eventsCalls
}

func (c *observedClient) committedIDs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.committed...)
}

// A Matrix delivery failure during catch-up holds the chat in the live
// session: the acquired page is retried without another login or MCHATLOGS, and
// its commit precedes the chat's live commits.
func TestScriptedBackendCatchUpFailureReplaysBeforeLiveAndSendsOnce(t *testing.T) {
	statePath := newIntegrationProfile(t)
	checkpoint, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitMessage(testChatID, 1); err != nil {
		t.Fatal(err)
	}

	trigger := make(chan struct{})
	// The script admits one login and one MCHATLOGS.
	dialers, backends, writes := scriptedScenario(t, 2, trigger, true)
	raw, err := client.OpenWithTestDialers(statePath, nil, dialers, shadowCheckedOptions())
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedClient{kakaoClient: raw}
	var opens int
	kakao, harness := newTestClient(t, func() (kakaoClient, error) {
		opens++
		return observed, nil
	})
	t.Cleanup(kakao.Disconnect)
	kakao.deliveryRetryDelays = []time.Duration{300 * time.Millisecond}
	harness.results = []bridgev2.EventHandlingResult{bridgev2.EventHandlingResultFailed}
	kakao.Connect(context.Background())
	if !kakao.IsLoggedIn() || observed.subscriptions() != 1 {
		t.Fatalf("a failed catch-up delivery did not go live; state=%+v", harness.lastState())
	}
	if len(observed.committedIDs()) != 0 {
		t.Fatal("failed catch-up advanced progress")
	}
	// The live message waits behind the held replay.
	close(trigger)
	time.Sleep(50 * time.Millisecond)
	if got := observed.committedIDs(); len(got) != 0 || harness.queuedCount() != 1 {
		t.Fatalf("live delivery passed the held replay: commits=%v queued=%d", got, harness.queuedCount())
	}
	waitFor(t, func() bool { return len(observed.committedIDs()) == 2 })
	if got := observed.committedIDs(); got[0] != 2 || got[1] != 3 {
		t.Fatalf("commits = %v, want replay 2 before live 3", got)
	}
	if opens != 1 || harness.queuedCount() != 3 {
		t.Fatalf("opens = %d, queued = %d; want one login and three deliveries", opens, harness.queuedCount())
	}
	if _, err := kakao.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "ambiguous outbound")); err == nil {
		t.Fatal("ambiguous outbound unexpectedly succeeded")
	}
	if *writes != 1 {
		t.Fatalf("ambiguous outbound WRITE count = %d, want 1", *writes)
	}
	// Stop the connector supervisor before releasing the underlying session.
	// Closing only the raw client would schedule recovery beyond this test.
	kakao.Disconnect()
	waitBackends(t, backends)
	reloaded, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsCommitted(testChatID, 2) || !reloaded.IsCommitted(testChatID, 3) {
		t.Fatal("replayed and live events were not committed")
	}
	reopenedClient, err := client.OpenWithTestDialers(statePath, nil, dialers, shadowCheckedOptions())
	if err != nil {
		t.Fatal("profile lease was not released after shutdown")
	}
	if err := reopenedClient.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestScriptedBackendAutomaticRecoveryReleasesLeaseBeforeReopen(t *testing.T) {
	statePath := newIntegrationProfile(t)
	checkpoint, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitMessage(testChatID, 1); err != nil {
		t.Fatal(err)
	}
	outage := make(chan struct{})
	firstDialers, firstBackends, firstWrites := scriptedScenarioWithIDs(t, 2, outage, true, 2, 3, false)
	firstRaw, err := client.OpenWithTestDialers(statePath, nil, firstDialers, shadowCheckedOptions())
	if err != nil {
		t.Fatal(err)
	}
	live := make(chan struct{})
	secondDialers, secondBackends, _ := scriptedScenarioWithIDs(t, 3, live, false, 3, 4, true)
	var openMu sync.Mutex
	var opened int
	var clients []*observedClient
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		openMu.Lock()
		defer openMu.Unlock()
		opened++
		var raw *client.Client
		var openErr error
		switch opened {
		case 1:
			raw = firstRaw
		case 2:
			raw, openErr = client.OpenWithTestDialers(statePath, nil, secondDialers, shadowCheckedOptions())
		default:
			return nil, fmt.Errorf("unexpected automatic recovery open %d", opened)
		}
		if openErr != nil {
			return nil, openErr
		}
		observed := &observedClient{kakaoClient: raw}
		clients = append(clients, observed)
		return observed, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(context.Background())
	waitFor(t, func() bool {
		openMu.Lock()
		defer openMu.Unlock()
		return len(clients) == 1 && clients[0].subscriptions() == 1
	})
	// The first outage accepts a WRITE and then drops the carriage. Recovery
	// must shut down this owner before the second OpenWithTestDialers acquires
	// the lease, and it must never resend that ambiguous mutation.
	close(outage)
	waitFor(t, func() bool { return harness.queuedCount() >= 1 })
	if _, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "accepted then dropped")); err == nil {
		t.Fatal("ambiguous outbound unexpectedly succeeded")
	}
	if *firstWrites != 1 {
		t.Fatalf("automatic outage WRITE count = %d, want 1", *firstWrites)
	}
	waitFor(t, func() bool {
		openMu.Lock()
		defer openMu.Unlock()
		return opened == 2 && len(clients) == 2 && clients[1].subscriptions() == 1
	})
	ids := clients[1].committedIDs()
	if len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("automatic catch-up commits = %v, want [3] before live subscription", ids)
	}
	replayed, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IsCommitted(testChatID, 3) || replayed.IsCommitted(testChatID, 4) {
		t.Fatal("automatic catch-up did not commit log 3 before live log 4")
	}
	close(live)
	waitFor(t, func() bool { return harness.queuedCount() >= 3 })
	if got := clients[1].committedIDs(); len(got) != 2 || got[1] != 4 {
		t.Fatalf("automatic live commit order = %v, want [3 4]", got)
	}
	kc.Disconnect()
	waitBackends(t, firstBackends)
	// The recovery reuses the GETCONF reply the first login stored, so the
	// second booking backend is never dialed.
	for i, backend := range secondBackends[1:] {
		if backendErr := backend.Wait(2 * time.Second); backendErr != nil {
			t.Fatalf("second backend %d: %v", i+1, backendErr)
		}
	}
}

func newIntegrationProfile(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.json")
	store, err := authstate.Create(path, authstate.Config{DeviceName: "integration", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCredentials(authstate.Credentials{UserID: 1, AccessToken: "test-access", AutoLoginMaterial: []byte("synthetic-material")}); err != nil {
		t.Fatal(err)
	}
	return path
}

func scriptedScenario(t *testing.T, maxLogID int64, trigger <-chan struct{}, withWrite bool) (client.TestDialers, []*testloco.Backend, *int) {
	return scriptedScenarioWithIDs(t, maxLogID, trigger, withWrite, 2, 3, true)
}

func scriptedScenarioWithIDs(t *testing.T, maxLogID int64, trigger <-chan struct{}, withWrite bool, replayLogID, liveLogID int64, pushLive bool) (client.TestDialers, []*testloco.Backend, *int) {
	t.Helper()
	loginReply := bson.D{{Key: "status", Value: int32(0)}, {Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: testChatID}, {Key: "l", Value: bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: maxLogID}}}}}}, {Key: "eof", Value: true}, {Key: "lastTokenId", Value: int64(10)}, {Key: "lbk", Value: int32(1)}}
	catchUpReply := bson.D{{Key: "status", Value: int32(0)}, {Key: "eof", Value: true}, {Key: "chatLogs", Value: bson.A{bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: replayLogID}, {Key: "prevId", Value: replayLogID - 1}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "replay"}}}}}
	steps := []testloco.Step{testloco.Expect("LOGINLIST", nil, loginReply)}
	steps = append(steps, testloco.Expect("MCHATLOGS", nil, catchUpReply))
	writes := new(int)
	if trigger == nil {
		steps = append(steps, testloco.HoldOpen())
	} else if pushLive {
		steps = append(steps, testloco.PushAfter(trigger, "MSG", bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: liveLogID}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "live"}}}}))
		if withWrite {
			steps = append(steps, testloco.DropAfter("WRITE", writes))
		}
		steps = append(steps, testloco.HoldOpen())
	} else if withWrite {
		steps = append(steps, testloco.DropAfter("WRITE", writes))
		steps = append(steps, testloco.HoldOpen())
	}
	dialers, backends := scriptedDialers(t, steps...)
	return dialers, backends, writes
}

// scriptedDialers serves booking and checkin and runs steps on the secure
// carriage.
func scriptedDialers(t *testing.T, steps ...testloco.Step) (client.TestDialers, []*testloco.Backend) {
	t.Helper()
	booking, err := testloco.NewBackend(false, testloco.Expect("GETCONF", nil, bson.D{{Key: "status", Value: int32(0)}, {Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}}, {Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}}}))
	if err != nil {
		t.Fatal(err)
	}
	checkin, err := testloco.NewBackend(false, testloco.Expect("CHECKIN", nil, bson.D{{Key: "status", Value: int32(0)}, {Key: "host", Value: "carriage.invalid"}, {Key: "port", Value: int32(995)}}))
	if err != nil {
		t.Fatal(err)
	}
	carriage, err := testloco.NewBackend(true, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = booking.Endpoint.Client.Close()
		_ = checkin.Endpoint.Client.Close()
		_ = carriage.Endpoint.Client.Close()
	})
	dialers := client.TestDialers{
		TLS:    testloco.Hosts{"booking-loco.kakao.com": booking, "checkin.invalid": checkin}.Dial,
		Secure: testloco.Hosts{"carriage.invalid": carriage}.Dial,
	}
	return dialers, []*testloco.Backend{booking, checkin, carriage}
}

func waitBackends(t *testing.T, backends []*testloco.Backend) {
	t.Helper()
	for _, backend := range backends {
		if err := backend.Wait(2 * time.Second); err != nil {
			t.Fatal(err)
		}
	}
}
