package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

func TestClientReactDoesNotOpenLocoSession(t *testing.T) {
	state := reusableTestState()
	requests := 0
	doer := friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Path != "/messaging/chats/42/bubble/reactions" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":0}`))}, nil
	})
	api, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	api.dial = func(_ context.Context, _ authstate.State) (*Session, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	response, err := api.React(t.Context(), reactions.Request{ChatID: 42, LogID: 99, Type: reactions.Heart})
	if err != nil || response.Status != 0 {
		t.Fatalf("React response=%#v err=%v", response, err)
	}
	if requests != 1 || dials != 0 {
		t.Fatalf("requests=%d dials=%d", requests, dials)
	}
}

func TestClientReactionMembersDoesNotOpenLocoSession(t *testing.T) {
	state := reusableTestState()
	doer := friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/messaging/chats/42/bubble/reactions/99/members" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"1":[7],"revision":4}`))}, nil
	})
	api, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	api.dial = func(_ context.Context, _ authstate.State) (*Session, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	members, err := api.ReactionMembers(t.Context(), 42, 99)
	if err != nil || members.Revision != 4 || members.Members[reactions.Heart][0] != 7 {
		t.Fatalf("members=%#v err=%v", members, err)
	}
	if dials != 0 {
		t.Fatalf("dials=%d", dials)
	}
}
