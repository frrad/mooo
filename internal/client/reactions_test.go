package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/events"
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

func TestClientReactionDetailsMergesMacSourcesWithoutLocoSession(t *testing.T) {
	state := reusableTestState()
	var mu sync.Mutex
	var paths []string
	doer := friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		switch req.URL.Path {
		case "/messaging/chats/42/bubble/reactions/99/members":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"1":[7],"revision":4}`))}, nil
		case "/emoticon/chat/rx/log-details":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":0,"details":[{"k":1,"o":"1","u":["8"]},{"k":2,"o":"1200509","u":["9"]}]}`))}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
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
	details, err := api.ReactionDetails(t.Context(), events.ReactionChanged{
		ChatID: 42, LogID: 99, Revision: 4,
		Items: []events.ReactionItem{{ID: "1", Kind: 1, Count: 1}, {ID: "1200509", Kind: 2, Count: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 2 || details[0].Source != reactions.DetailSourceLegacy || len(details[0].UserIDs) != 2 || details[0].UserIDs[1] != 8 || details[1].Source != reactions.DetailSourceMini || details[1].UserIDs[0] != 9 {
		t.Fatalf("details = %#v", details)
	}
	mu.Lock()
	requestCount := len(paths)
	mu.Unlock()
	if requestCount != 2 || dials != 0 {
		t.Fatalf("requests=%d paths=%v dials=%d", requestCount, paths, dials)
	}
}

func TestClientReactionMembersDoesNotOpenLocoSession(t *testing.T) {
	state := reusableTestState()
	doer := friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/messaging/chats/42/bubble/reactions/99/members" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"1":[7],"revision":4,"future":{"shape":true}}`))}, nil
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
	if string(members.Fields["future"]) != `{"shape":true}` {
		t.Fatalf("raw fields = %#v", members.Fields)
	}
	if dials != 0 {
		t.Fatalf("dials=%d", dials)
	}
}
