package client

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

func accountProfile(t *testing.T, root, name string, account int64, policy HistoryPacing) *Client {
	t.Helper()
	path := filepath.Join(root, name)
	store, err := authstate.Create(path, authstate.Config{DeviceName: "synthetic", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InstallCredentials(authstate.Credentials{UserID: account, AccessToken: "synthetic", AutoLoginMaterial: []byte("synthetic")}); err != nil {
		t.Fatal(err)
	}
	c, err := OpenWithOptions(path, nil, OpenOptions{HistoryPacing: policy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return c
}
func accountRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestAccountHistoryProfilesSerializeButDoNotShareResponses(t *testing.T) {
	root := accountRoot(t)
	policy := HistoryPacing{MinimumInterval: 60 * time.Millisecond, ErrorBackoff: 200 * time.Millisecond}
	first := accountProfile(t, root, "first", 1000, policy)
	second := accountProfile(t, root, "second", 1000, policy)
	if first.historyAdmission.shared != second.historyAdmission.shared {
		t.Fatal("profiles have separate account gates")
	}
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 2)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var completed atomic.Int64
	go func() {
		_, err := first.historyAdmission.run(t.Context(), req, func(ctx context.Context) (syncmsg.Response, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return syncmsg.Response{}, ctx.Err()
			}
			completed.Store(time.Now().UnixNano())
			return admissionTestResponse(t, 101), nil
		})
		done <- err
	}()
	<-entered
	dispatched := make(chan struct{})
	go func() {
		response, err := second.historyAdmission.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) {
			if elapsed := time.Since(time.Unix(0, completed.Load())); elapsed < policy.MinimumInterval {
				t.Error("account spacing shortened", elapsed)
			}
			close(dispatched)
			return admissionTestResponse(t, 103), nil
		})
		if err == nil {
			last, _ := syncmsg.LogID(response.ChatLogs[0])
			if last != 103 {
				t.Error("another device's response was coalesced")
			}
		}
		done <- err
	}()
	waitHistoryDemands(t, second.historyAdmission, 1)
	select {
	case <-dispatched:
		t.Fatal("parallel account dispatch")
	default:
	}
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func TestAccountHistoryPendingBoundSharedAcrossProfiles(t *testing.T) {
	root := accountRoot(t)
	policy := HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond, MaxPending: 2}
	a := accountProfile(t, root, "a", 1000, policy)
	b := accountProfile(t, root, "b", 1000, policy)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 2)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	go func() {
		_, err := a.historyAdmission.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) {
			close(entered)
			<-release
			return admissionTestResponse(t, 103), nil
		})
		done <- err
	}()
	<-entered
	go func() {
		_, err := b.historyAdmission.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) { return admissionTestResponse(t, 103), nil })
		done <- err
	}()
	waitHistoryDemands(t, b.historyAdmission, 1)
	if _, err := b.historyAdmission.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) {
		t.Error("excess demand dispatched")
		return syncmsg.Response{}, nil
	}); !errors.Is(err, ErrHistoryBusy) {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func TestAccountHistoryLegacyCooldownSurvivesDifferentProfileRestart(t *testing.T) {
	root := accountRoot(t)
	until := time.Now().Add(time.Second)
	if err := privatejson.WriteAtomic(filepath.Join(root, "older.history-admission"), historyAdmissionState{Version: 1, AccountID: 1000, NextAllowed: until}); err != nil {
		t.Fatal(err)
	}
	profile := accountProfile(t, root, "newer", 1000, HistoryPacing{})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if _, err := profile.historyAdmission.run(ctx, req, func(context.Context) (syncmsg.Response, error) {
		t.Error("legacy account cooldown bypassed")
		return syncmsg.Response{}, nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	path := profile.historyAdmission.shared.path
	state, found, err := privatejson.Read[historyAdmissionState](path)
	if err != nil || !found || !state.NextAllowed.Equal(until) {
		t.Fatal("migration reservation lost", found, err)
	}
	if err := profile.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	another := accountProfile(t, root, "another", 1000, HistoryPacing{})
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel2()
	if _, err := another.historyAdmission.run(ctx2, req, func(context.Context) (syncmsg.Response, error) {
		t.Error("restart forgot cooldown")
		return syncmsg.Response{}, nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestAccountHistoryClosingOneProfileDoesNotStopAnother(t *testing.T) {
	root := accountRoot(t)
	policy := HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: 20 * time.Millisecond}
	a := accountProfile(t, root, "a", 1000, policy)
	b := accountProfile(t, root, "b", 1000, policy)
	entered := make(chan struct{})
	done := make(chan error, 1)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	go func() {
		_, err := a.historyAdmission.run(t.Context(), req, func(ctx context.Context) (syncmsg.Response, error) {
			close(entered)
			<-ctx.Done()
			return syncmsg.Response{}, ctx.Err()
		})
		done <- err
	}()
	<-entered
	if err := a.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("stopped request completed")
	}
	if _, err := b.historyAdmission.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) { return admissionTestResponse(t, 103), nil }); err != nil {
		t.Fatal(err)
	}
}

func TestAccountHistorySubprocessHelper(t *testing.T) {
	root := os.Getenv("MOOO_ACCOUNT_PACER_TEST_ROOT")
	if root == "" {
		t.Skip("subprocess only")
	}
	c := accountProfile(t, root, "child", 1000, HistoryPacing{MinimumInterval: 50 * time.Millisecond, ErrorBackoff: 200 * time.Millisecond})
	if err := os.WriteFile(filepath.Join(root, "child-ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, err := c.historyAdmission.run(ctx, syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}, func(context.Context) (syncmsg.Response, error) {
		if err := os.WriteFile(filepath.Join(root, "child-dispatched"), []byte("dispatched"), 0600); err != nil {
			t.Fatal(err)
		}
		return admissionTestResponse(t, 103), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestAccountHistoryLeaseSerializesSeparateProcesses(t *testing.T) {
	root := accountRoot(t)
	c := accountProfile(t, root, "parent", 1000, HistoryPacing{MinimumInterval: 50 * time.Millisecond, ErrorBackoff: 200 * time.Millisecond})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 1)
	go func() {
		_, err := c.historyAdmission.run(t.Context(), syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}, func(context.Context) (syncmsg.Response, error) {
			close(entered)
			<-release
			return admissionTestResponse(t, 103), nil
		})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAccountHistorySubprocessHelper$")
	cmd.Env = append(os.Environ(), "MOOO_ACCOUNT_PACER_TEST_ROOT="+root)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan error, 1)
	go func() { childDone <- cmd.Wait() }()
	waitForCondition(t, func() bool { _, err := os.Stat(filepath.Join(root, "child-ready")); return err == nil })
	if _, err := os.Stat(filepath.Join(root, "child-dispatched")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("other process bypassed account lease")
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-childDone; err != nil {
		t.Fatal(err, output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "child-dispatched")); err != nil {
		t.Fatal("child never dispatched", err)
	}
}
func TestAccountHistoryExplicitRootAndPolicyConflict(t *testing.T) {
	scheduler := accountRoot(t)
	one := accountRoot(t)
	two := accountRoot(t)
	policy := HistoryPacing{StateDirectory: scheduler}
	a := accountProfile(t, one, "one", 1000, policy)
	b := accountProfile(t, two, "two", 1000, policy)
	if a.historyAdmission.shared != b.historyAdmission.shared {
		t.Fatal("explicit root did not share account")
	}
	conflicting := policy
	conflicting.MinimumInterval = 10 * time.Second
	if admission, err := openAccountHistoryAdmission(filepath.Join(two, "another"), 1000, conflicting); err == nil {
		admission.stop()
		t.Fatal("mixed live account policies accepted")
	}
	other := accountProfile(t, one, "other-account", 2000, policy)
	if other.historyAdmission.shared == a.historyAdmission.shared {
		t.Fatal("different accounts shared one queue")
	}
}

func TestAccountHistoryMoreConservativePolicyHonorsPreviousCompletion(t *testing.T) {
	root := accountRoot(t)
	fast := accountProfile(t, root, "fast", 1000, HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond})
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if _, err := fast.historyAdmission.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) { return admissionTestResponse(t, 103), nil }); err != nil {
		t.Fatal(err)
	}
	if err := fast.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	slower := accountProfile(t, root, "slower", 1000, HistoryPacing{MinimumInterval: time.Second, ErrorBackoff: time.Second})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := slower.historyAdmission.run(ctx, req, func(context.Context) (syncmsg.Response, error) {
		t.Error("new conservative spacing bypassed last completion")
		return syncmsg.Response{}, nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestAccountHistoryRejectsAmbiguousRootsWhileAccountActive(t *testing.T) {
	accountProfile(t, accountRoot(t), "one", 1000, HistoryPacing{})
	admission, err := openAccountHistoryAdmission(filepath.Join(accountRoot(t), "two"), 1000, HistoryPacing{})
	if err == nil {
		admission.stop()
		t.Fatal("same account silently used two scheduler roots")
	}
}
