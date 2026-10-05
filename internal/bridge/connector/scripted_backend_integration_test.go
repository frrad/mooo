package connector

import (
	"context"
	"errors"
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
	protocol "github.com/frrad/mooo/internal/protocol/loco"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

type observedClient struct {
	kakaoClient
	mu          sync.Mutex
	eventsCalls int
}

func (c *observedClient) Events(ctx context.Context) (<-chan events.Result, error) {
	c.mu.Lock()
	c.eventsCalls++
	c.mu.Unlock()
	return c.kakaoClient.Events(ctx)
}

func (c *observedClient) subscriptions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.eventsCalls
}

func TestScriptedBackendCatchUpFailureReplaysBeforeLiveAndSendsOnce(t *testing.T) {
	statePath := newIntegrationProfile(t)
	checkpoint, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitMessage(testChatID, 1); err != nil {
		t.Fatal(err)
	}

	firstDialers, firstBackends, _ := scriptedScenario(t, 2, nil, false)
	firstRaw, err := client.OpenWithTestDialers(statePath, nil, firstDialers)
	if err != nil {
		t.Fatal(err)
	}
	firstClient := &observedClient{kakaoClient: firstRaw}
	kakao1, harness1 := newTestClient(t, func() (kakaoClient, error) { return firstClient, nil })
	harness1.results = []bridgev2.EventHandlingResult{bridgev2.EventHandlingResultFailed}
	kakao1.Connect(context.Background())
	if got := harness1.lastState(); got.StateEvent != status.StateTransientDisconnect || got.Error != stateConnectFailed {
		t.Fatalf("failed catch-up state = %+v", got)
	}
	if got := firstClient.subscriptions(); got != 0 {
		t.Fatalf("Events calls after failed catch-up = %d, want 0", got)
	}
	if err := firstRaw.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, firstBackends)
	reloaded, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.IsCommitted(testChatID, 2) {
		t.Fatal("failed catch-up advanced the checkpoint")
	}

	trigger := make(chan struct{})
	secondDialers, secondBackends, writes := scriptedScenario(t, 2, trigger, true)
	secondRaw, err := client.OpenWithTestDialers(statePath, nil, secondDialers)
	if err != nil {
		t.Fatal(err)
	}
	secondClient := &observedClient{kakaoClient: secondRaw}
	kakao2, harness2 := newTestClient(t, func() (kakaoClient, error) { return secondClient, nil })
	kakao2.Connect(context.Background())
	if got := secondClient.subscriptions(); got != 1 {
		for _, backend := range secondBackends {
			if backendErr := backend.Wait(100 * time.Millisecond); backendErr != nil {
				t.Logf("backend: %v", backendErr)
			}
		}
		t.Fatalf("Events calls after successful catch-up = %d, want 1; state=%+v queued=%d", got, harness2.lastState(), harness2.queuedCount())
	}
	replayed, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IsCommitted(testChatID, 2) || replayed.IsCommitted(testChatID, 3) {
		t.Fatal("catch-up checkpoint was not committed before live delivery")
	}
	close(trigger)
	deadline := time.After(2 * time.Second)
	for harness2.queuedCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("replay/live delivery count = %d, want 2", harness2.queuedCount())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := kakao2.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "ambiguous outbound")); err == nil {
		t.Fatal("ambiguous outbound unexpectedly succeeded")
	}
	if *writes != 1 {
		t.Fatalf("ambiguous outbound WRITE count = %d, want 1", *writes)
	}
	if err := secondRaw.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, secondBackends)
	reloaded, err = continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsCommitted(testChatID, 2) || !reloaded.IsCommitted(testChatID, 3) {
		t.Fatal("replayed and live events were not committed")
	}
	reopenedClient, err := client.OpenWithTestDialers(statePath, nil, secondDialers)
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
	firstDialers, firstBackends, _ := scriptedScenario(t, 2, nil, false)
	firstRaw, err := client.OpenWithTestDialers(statePath, nil, firstDialers)
	if err != nil {
		t.Fatal(err)
	}
	secondDialers, secondBackends := scriptedLiveScenario(t, 2)
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
			raw, openErr = client.OpenWithTestDialers(statePath, nil, secondDialers)
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
	// Closing the scripted carriage ends the live session. Recovery must shut
	// down this owner before the second OpenWithTestDialers acquires the lease.
	if err := firstBackends[2].Endpoint.Client.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		openMu.Lock()
		defer openMu.Unlock()
		return opened == 2 && len(clients) == 2 && clients[1].subscriptions() == 1
	})
	if got := harness.queuedCount(); got < 1 {
		t.Fatalf("automatic recovery queued %d events, want catch-up delivery", got)
	}
	kc.Disconnect()
	waitBackends(t, firstBackends)
	for i, backend := range secondBackends {
		if backendErr := backend.Wait(2 * time.Second); backendErr != nil {
			t.Fatalf("second backend %d: %v", i, backendErr)
		}
	}
}

func scriptedLiveScenario(t *testing.T, maxLogID int64) (client.TestDialers, []*testloco.Backend) {
	t.Helper()
	booking, err := testloco.NewBackend(false, requestStep("GETCONF", bson.D{{Key: "status", Value: int32(0)}, {Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}}, {Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}}}))
	if err != nil {
		t.Fatal(err)
	}
	checkin, err := testloco.NewBackend(false, requestStep("CHECKIN", bson.D{{Key: "status", Value: int32(0)}, {Key: "host", Value: "carriage.invalid"}, {Key: "port", Value: int32(995)}}))
	if err != nil {
		t.Fatal(err)
	}
	loginReply := bson.D{{Key: "status", Value: int32(0)}, {Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: testChatID}, {Key: "l", Value: bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: maxLogID}}}}}}, {Key: "eof", Value: true}, {Key: "lastTokenId", Value: int64(10)}, {Key: "lbk", Value: int32(1)}}
	carriage, err := testloco.NewBackend(true, requestStep("LOGINLIST", loginReply), holdStep())
	if err != nil {
		t.Fatal(err)
	}
	backends := []*testloco.Backend{booking, checkin, carriage}
	t.Cleanup(func() {
		for _, backend := range backends {
			_ = backend.Endpoint.Client.Close()
		}
	})
	dialers := client.TestDialers{
		TLS: func(_ context.Context, host string, _ int) (client.TestConnection, error) {
			switch host {
			case "booking-loco.kakao.com":
				return client.TestConnection{Conn: booking.Endpoint.Client}, nil
			case "checkin.invalid":
				return client.TestConnection{Conn: checkin.Endpoint.Client}, nil
			default:
				return client.TestConnection{}, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		Secure: func(_ context.Context, host string, _ int) (client.TestConnection, error) {
			if host != "carriage.invalid" {
				return client.TestConnection{}, fmt.Errorf("unexpected secure host %q", host)
			}
			return client.TestConnection{Conn: carriage.Endpoint.Client, Secure: carriage.Endpoint.ClientSecure}, nil
		},
	}
	return dialers, backends
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
	t.Helper()
	booking, err := testloco.NewBackend(false, requestStep("GETCONF", bson.D{{Key: "status", Value: int32(0)}, {Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}}, {Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}}}))
	if err != nil {
		t.Fatal(err)
	}
	checkin, err := testloco.NewBackend(false, requestStep("CHECKIN", bson.D{{Key: "status", Value: int32(0)}, {Key: "host", Value: "carriage.invalid"}, {Key: "port", Value: int32(995)}}))
	if err != nil {
		t.Fatal(err)
	}
	loginReply := bson.D{{Key: "status", Value: int32(0)}, {Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: testChatID}, {Key: "l", Value: bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: maxLogID}}}}}}, {Key: "eof", Value: true}, {Key: "lastTokenId", Value: int64(10)}, {Key: "lbk", Value: int32(1)}}
	syncReply := bson.D{{Key: "status", Value: int32(0)}, {Key: "chatLogs", Value: bson.A{bson.D{{Key: "logId", Value: int64(2)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "replay"}}}}}
	steps := []testloco.Step{requestStep("LOGINLIST", loginReply), requestStep("SYNCMSG", syncReply)}
	writes := new(int)
	if trigger == nil {
		steps = append(steps, holdStep())
	} else {
		steps = append(steps, pushAfter(trigger, "MSG", bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(3)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "live"}}}}))
		if withWrite {
			steps = append(steps, writeDropStep(writes))
		}
		steps = append(steps, holdStep())
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
	dialers := client.TestDialers{TLS: func(_ context.Context, host string, _ int) (client.TestConnection, error) {
		switch host {
		case "booking-loco.kakao.com":
			return client.TestConnection{Conn: booking.Endpoint.Client}, nil
		case "checkin.invalid":
			return client.TestConnection{Conn: checkin.Endpoint.Client}, nil
		default:
			return client.TestConnection{}, fmt.Errorf("unexpected TLS host %q", host)
		}
	}, Secure: func(_ context.Context, host string, _ int) (client.TestConnection, error) {
		if host != "carriage.invalid" {
			return client.TestConnection{}, fmt.Errorf("unexpected secure host %q", host)
		}
		return client.TestConnection{Conn: carriage.Endpoint.Client, Secure: carriage.Endpoint.ClientSecure}, nil
	}}
	return dialers, []*testloco.Backend{booking, checkin, carriage}, writes
}

func requestStep(method string, reply bson.D) testloco.Step {
	return func(peer *testloco.Peer) error {
		request, err := peer.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != method {
			return fmt.Errorf("method = %q, want %q", request.Header.Method, method)
		}
		body, err := bson.Marshal(reply)
		if err != nil {
			return err
		}
		return peer.Write(protocol.Packet{Header: protocol.Header{PacketID: request.Header.PacketID, Method: method, BodyType: protocol.BodyTypeBSON}, Body: body})
	}
}
func pushAfter(trigger <-chan struct{}, method string, body bson.D) testloco.Step {
	return func(peer *testloco.Peer) error {
		select {
		case <-trigger:
		case <-time.After(2 * time.Second):
			return errors.New("push trigger timeout")
		}
		raw, err := bson.Marshal(body)
		if err != nil {
			return err
		}
		return peer.Write(protocol.Packet{Header: protocol.Header{Method: method, BodyType: protocol.BodyTypeBSON}, Body: raw})
	}
}
func writeDropStep(count *int) testloco.Step {
	return func(peer *testloco.Peer) error {
		request, err := peer.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "WRITE" {
			return fmt.Errorf("method = %q, want WRITE", request.Header.Method)
		}
		*count = *count + 1
		return peer.Conn.Close()
	}
}
func holdStep() testloco.Step {
	return func(peer *testloco.Peer) error {
		packet, err := peer.Read()
		if err != nil {
			return nil
		}
		return fmt.Errorf("unexpected packet %s", packet.Header.Method)
	}
}
func waitBackends(t *testing.T, backends []*testloco.Backend) {
	t.Helper()
	for _, backend := range backends {
		if err := backend.Wait(2 * time.Second); err != nil {
			t.Fatal(err)
		}
	}
}
