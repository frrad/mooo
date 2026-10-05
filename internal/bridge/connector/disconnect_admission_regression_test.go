package connector

import (
	"testing"
	"time"
)

func TestParentDisconnectAdmissionHasDeadline(t *testing.T) {
	previous := terminalDisconnectTimeout
	terminalDisconnectTimeout = 20 * time.Millisecond
	defer func() { terminalDisconnectTimeout = previous }()
	kc := &KakaoClient{disconnectGate: make(chan struct{}, 1)}
	kc.disconnectGate <- struct{}{}
	returned := make(chan struct{})
	go func() { kc.Disconnect(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		<-kc.disconnectGate
		<-returned
		t.Fatal("Disconnect waited indefinitely for occupied admission gate")
	}
	if len(kc.disconnectGate) != 1 {
		t.Fatal("waiting caller released another caller's gate")
	}
	<-kc.disconnectGate
}
