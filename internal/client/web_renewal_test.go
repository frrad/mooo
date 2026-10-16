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
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// renewableWebClient builds a Client whose persisted access token the web API
// already rejects, with a refresh token that renew_token.json accepts.
func renewableWebClient(t *testing.T, doer macweb.Doer) (*Client, *authstate.Store) {
	t.Helper()
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
	api, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	api.store = store
	api.dial = func(context.Context, authstate.State) (*session, error) {
		return nil, errors.New("unexpected dial")
	}
	t.Cleanup(func() { _ = api.Shutdown(context.Background()) })
	return api, store
}

func renewTokenResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
		`{"access_token":"rotated-access","refresh_token":"rotated-refresh","token_type":"bearer"}`,
	))}
}

func unauthorizedResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`))}
}

// Regression for #408: a web 401 while the LOCO session is healthy left the
// expired token in place until the process restarted.
func TestReactRenewsCredentialsAndRetriesOnceAfterHTTP401(t *testing.T) {
	var authorizations []string
	renewals := 0
	doer := friendDoerFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/mac/account/renew_token.json":
			renewals++
			return renewTokenResponse(), nil
		case "/messaging/chats/42/bubble/reactions":
			token, _, _ := strings.Cut(request.Header.Get("Authorization"), "-")
			authorizations = append(authorizations, token)
			if token == "expired" {
				return unauthorizedResponse(), nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":0}`))}, nil
		default:
			return nil, errors.New("unexpected HTTP path " + request.URL.Path)
		}
	})
	api, store := renewableWebClient(t, doer)

	response, err := api.React(t.Context(), reactions.Request{ChatID: 42, LogID: 99, Type: reactions.Heart})
	if err != nil || response.Status != 0 {
		t.Fatalf("React response=%#v err=%v", response, err)
	}
	if renewals != 1 {
		t.Fatalf("renewals = %d, want 1", renewals)
	}
	if len(authorizations) != 2 || authorizations[0] != "expired" || authorizations[1] != "rotated" {
		t.Fatalf("reaction Authorization tokens = %q, want expired then rotated", authorizations)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != "rotated-access" {
		t.Fatal("rotated access token was not persisted")
	}
}

func TestReactionMembersRenewsCredentialsAndRetriesOnceAfterHTTP401(t *testing.T) {
	var authorizations []string
	renewals := 0
	doer := friendDoerFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/mac/account/renew_token.json":
			renewals++
			return renewTokenResponse(), nil
		case "/messaging/chats/42/bubble/reactions/99/members":
			token, _, _ := strings.Cut(request.Header.Get("Authorization"), "-")
			authorizations = append(authorizations, token)
			if token == "expired" {
				return unauthorizedResponse(), nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"1":[7],"revision":4}`))}, nil
		default:
			return nil, errors.New("unexpected HTTP path " + request.URL.Path)
		}
	})
	api, _ := renewableWebClient(t, doer)

	members, err := api.ReactionMembers(t.Context(), 42, 99)
	if err != nil {
		t.Fatal(err)
	}
	if members.Revision != 4 || len(members.Members[reactions.Heart]) != 1 || members.Members[reactions.Heart][0] != 7 {
		t.Fatalf("members = %#v", members)
	}
	if renewals != 1 || len(authorizations) != 2 || authorizations[1] != "rotated" {
		t.Fatalf("renewals=%d authorizations=%q, want one renewal then a rotated retry", renewals, authorizations)
	}
}

func TestWebHTTP401AfterRenewalIsReturnedWithoutAnotherRenewal(t *testing.T) {
	reactionRequests := 0
	renewals := 0
	doer := friendDoerFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/mac/account/renew_token.json":
			renewals++
			return renewTokenResponse(), nil
		case "/messaging/chats/42/bubble/reactions":
			reactionRequests++
			return unauthorizedResponse(), nil
		default:
			return nil, errors.New("unexpected HTTP path " + request.URL.Path)
		}
	})
	api, _ := renewableWebClient(t, doer)

	_, err := api.React(t.Context(), reactions.Request{ChatID: 42, LogID: 99, Type: reactions.Heart})
	var status *macweb.StatusError
	if !errors.As(err, &status) || status.Code != http.StatusUnauthorized {
		t.Fatalf("React error = %v, want the retried HTTP 401", err)
	}
	if renewals != 1 || reactionRequests != 2 {
		t.Fatalf("renewals=%d reaction requests=%d, want 1 and 2", renewals, reactionRequests)
	}
}

func TestWebHTTP401RenewalFailureIsReturnedAsCredentialRenewal(t *testing.T) {
	reactionRequests := 0
	doer := friendDoerFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/mac/account/renew_token.json":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":-999}`))}, nil
		case "/messaging/chats/42/bubble/reactions":
			reactionRequests++
			return unauthorizedResponse(), nil
		default:
			return nil, errors.New("unexpected HTTP path " + request.URL.Path)
		}
	})
	api, store := renewableWebClient(t, doer)

	_, err := api.React(t.Context(), reactions.Request{ChatID: 42, LogID: 99, Type: reactions.Heart})
	if !errors.Is(err, ErrCredentialRenewal) || !errors.Is(err, ErrSignedOut) || !errors.Is(err, reactions.ErrRejected) {
		t.Fatalf("React error = %v, want the 401 joined with a signed-out credential renewal", err)
	}
	if reactionRequests != 1 {
		t.Fatalf("reaction requests = %d, want 1 when renewal fails", reactionRequests)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != "expired-access" {
		t.Fatal("failed renewal replaced the stored access token")
	}
}

func TestConcurrentWebHTTP401sShareOneRenewal(t *testing.T) {
	var mu sync.Mutex
	renewals := 0
	firstRejections := make(chan struct{})
	rejected := 0
	doer := friendDoerFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/mac/account/renew_token.json":
			mu.Lock()
			renewals++
			mu.Unlock()
			return renewTokenResponse(), nil
		case "/messaging/chats/42/bubble/reactions":
			if strings.HasPrefix(request.Header.Get("Authorization"), "expired-") {
				// Hold both calls until each has been rejected with the
				// expired token, so both observe the same 401.
				mu.Lock()
				rejected++
				if rejected == 2 {
					close(firstRejections)
				}
				mu.Unlock()
				<-firstRejections
				return unauthorizedResponse(), nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":0}`))}, nil
		default:
			return nil, errors.New("unexpected HTTP path " + request.URL.Path)
		}
	})
	api, _ := renewableWebClient(t, doer)

	var wait sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, errs[i] = api.React(t.Context(), reactions.Request{ChatID: 42, LogID: int64(99 + i), Type: reactions.Heart})
		}()
	}
	wait.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("React %d error = %v", i, err)
		}
	}
	if renewals != 1 {
		t.Fatalf("renewals = %d, want one renewal shared by both calls", renewals)
	}
}
