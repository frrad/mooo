package connector

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
)

func TestOperatorReconnectRestartsAStoppedLogin(t *testing.T) {
	connected := &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return &fakeKakao{connectErr: client.StatusError{Command: "LOGINLIST", Status: -445}}, nil
		}
		return connected, nil
	})
	kc.wait = func(context.Context, time.Duration) error {
		t.Error("stopped login scheduled a retry")
		return nil
	}
	kc.Connect(context.Background())
	if kc.IsLoggedIn() || opens.Load() != 1 {
		t.Fatalf("login rejection: logged in %v after %d opens, want stopped after 1", kc.IsLoggedIn(), opens.Load())
	}

	if got := kc.operatorReconnect(); got != reconnectStarted {
		t.Fatalf("operatorReconnect() = %v, want reconnectStarted", got)
	}
	waitFor(t, kc.IsLoggedIn)
	if got := opens.Load(); got != 2 {
		t.Fatalf("opens = %d, want 2", got)
	}
	kc.Disconnect()
}

func TestOperatorReconnectLeavesAConnectedLoginAlone(t *testing.T) {
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opens.Add(1)
		return &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}, nil
	})
	kc.Connect(context.Background())
	if got := kc.operatorReconnect(); got != reconnectAlreadyConnected {
		t.Fatalf("operatorReconnect() = %v, want reconnectAlreadyConnected", got)
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("opens = %d, want 1", got)
	}
	kc.Disconnect()
}

func TestOperatorReconnectDoesNotCutAScheduledRetryShort(t *testing.T) {
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opens.Add(1)
		return &fakeKakao{connectErr: io.EOF}, nil
	})
	scheduled := make(chan struct{})
	var once sync.Once
	kc.wait = func(ctx context.Context, _ time.Duration) error {
		once.Do(func() { close(scheduled) })
		<-ctx.Done()
		return ctx.Err()
	}
	kc.Connect(context.Background())
	<-scheduled
	if got := kc.operatorReconnect(); got != reconnectPending {
		t.Fatalf("operatorReconnect() = %v, want reconnectPending", got)
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("opens = %d, want 1: a scheduled retry may be waiting out a rate limit", got)
	}
	kc.Disconnect()
}

func TestOperatorReconnectRefusesAKickedOutLogin(t *testing.T) {
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		t.Error("reconnect opened a kicked-out profile")
		return nil, io.EOF
	})
	kc.login.Metadata.(*UserLoginMetadata).KickedOut = &kickoutRecord{Reason: 1}
	if got := kc.operatorReconnect(); got != reconnectKickedOut {
		t.Fatalf("operatorReconnect() = %v, want reconnectKickedOut", got)
	}
}
