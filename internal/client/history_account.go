package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
)

// Account scheduling is shared; responses remain local to the device/session.
// A stable file lease covers reload, cooldown waiting, dispatch and publication.
type historyAccountGate struct {
	legacyFloor   time.Time
	accountID     int64
	mu            sync.Mutex
	pending, refs int
	gate          chan struct{}
	path          string
	policy        HistoryPacing
	failed        error // Gate owner only.
}

var historyAccounts = struct {
	sync.Mutex
	gates map[string]*historyAccountGate
}{gates: map[string]*historyAccountGate{}}

func openAccountHistoryAdmission(profile string, accountID int64, policy HistoryPacing) (*historyAdmission, error) {
	a, err := openHistoryAdmission(profile+".history-admission", accountID, policy)
	if err != nil {
		return nil, err
	}
	dir := policy.StateDirectory
	if dir == "" {
		dir = filepath.Dir(profile)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, ErrProtocol
	}
	if err = privatejson.ValidatePrivateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".history-account-"+historyDigest(strconv.FormatInt(accountID, 10))+".json")
	// Fail at open too, without creating network demand. Legacy cooldown is also
	// retained until the next account lease imports it into the shared record.
	state, found, err := privatejson.Read[historyAdmissionState](path)
	if err != nil {
		return nil, err
	}
	if found && !validHistoryAdmissionState(state, accountID) {
		return nil, privatejson.ErrCorrupt
	}
	a.policy.StateDirectory = dir
	historyAccounts.Lock()
	for otherPath, gate := range historyAccounts.gates {
		if gate.accountID == accountID && otherPath != path {
			historyAccounts.Unlock()
			return nil, errors.New("client: active account scheduler roots differ")
		}
	}
	shared := historyAccounts.gates[path]
	if shared != nil && shared.policy != a.policy {
		historyAccounts.Unlock()
		return nil, errors.New("client: active account history policies differ")
	}
	if shared == nil {
		shared = &historyAccountGate{accountID: accountID, gate: make(chan struct{}, 1), path: path, policy: a.policy}
		historyAccounts.gates[path] = shared
	}
	shared.refs++
	shared.mu.Lock()
	shared.legacyFloor = maxHistoryTime(shared.legacyFloor, a.state.NextAllowed)
	shared.mu.Unlock()
	historyAccounts.Unlock()
	a.shared = shared
	return a, nil
}
func validHistoryAdmissionState(state historyAdmissionState, accountID int64) bool {
	return state.Version == 1 && state.AccountID == accountID && !state.NextAllowed.IsZero() && !state.NextAllowed.After(time.Now().Add(24*time.Hour)) && !state.LastFinishedAt.After(time.Now().Add(24*time.Hour)) && !state.LastErrorAt.After(time.Now().Add(24*time.Hour))
}
func (a *historyAdmission) releaseAccount() {
	a.releaseOnce.Do(func() {
		if a.shared == nil {
			return
		}
		historyAccounts.Lock()
		defer historyAccounts.Unlock()
		a.shared.refs--
		if a.shared.refs == 0 {
			delete(historyAccounts.gates, a.shared.path)
		}
	})
}

func (a *historyAdmission) reserveDemand() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped || a.stopCtx.Err() != nil {
		return ErrClientClosed
	}
	if a.pending >= a.policy.MaxPending {
		return ErrHistoryBusy
	}
	if a.shared != nil {
		a.shared.mu.Lock()
		defer a.shared.mu.Unlock()
		if a.shared.pending >= a.policy.MaxPending {
			return ErrHistoryBusy
		}
		a.shared.pending++
	}
	a.pending++
	return nil
}
func (a *historyAdmission) finishDemand() {
	a.mu.Lock()
	if a.shared != nil {
		a.shared.mu.Lock()
		a.shared.pending--
		a.shared.mu.Unlock()
	}
	a.pending--
	idle := a.stopped && a.pending == 0
	a.mu.Unlock()
	if idle {
		a.finishStopped()
	}
}
func (a *historyAdmission) acquireAccount(ctx context.Context) (func(), error) {
	if a.shared == nil {
		return func() {}, nil
	}
	shared := a.shared
	select {
	case shared.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.stopCtx.Done():
		return nil, ErrClientClosed
	}
	releaseGate := func() { <-shared.gate }
	if shared.failed != nil {
		releaseGate()
		return nil, shared.failed
	}
	if err := privatejson.ValidatePrivateDir(filepath.Dir(shared.path)); err != nil {
		releaseGate()
		return nil, err
	}
	var lease *profileLease
	for {
		var err error
		lease, err = acquireProfileLease(shared.path + ".lock")
		if err == nil {
			break
		}
		if !errors.Is(err, errProfileInUse) {
			releaseGate()
			return nil, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			releaseGate()
			return nil, ctx.Err()
		case <-a.stopCtx.Done():
			timer.Stop()
			releaseGate()
			return nil, ErrClientClosed
		case <-timer.C:
		}
	}
	release := func() {
		if err := lease.Close(); err != nil {
			shared.failed = err
		}
		releaseGate()
	}
	state, found, err := privatejson.Read[historyAdmissionState](shared.path)
	if err != nil || (found && !validHistoryAdmissionState(state, a.state.AccountID)) {
		release()
		if err == nil {
			err = privatejson.ErrCorrupt
		}
		return nil, err
	}
	if found {
		a.state = state
	}
	shared.mu.Lock()
	floor := shared.legacyFloor
	shared.mu.Unlock()
	a.state.NextAllowed = maxHistoryTime(a.state.NextAllowed, floor)
	// Import existing sibling profile cooldowns before any account request. A
	// canceled waiter must not let another profile forget the old reservation.
	entries, err := os.ReadDir(filepath.Dir(a.path))
	if err != nil || len(entries) > 4096 {
		release()
		return nil, privatejson.ErrCorrupt
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".history-admission") {
			continue
		}
		legacy, exists, err := privatejson.Read[historyAdmissionState](filepath.Join(filepath.Dir(a.path), entry.Name()))
		if err != nil {
			release()
			return nil, err
		}
		if exists && legacy.AccountID == a.state.AccountID {
			if !validHistoryAdmissionState(legacy, a.state.AccountID) {
				release()
				return nil, privatejson.ErrCorrupt
			}
			a.state.NextAllowed = maxHistoryTime(a.state.NextAllowed, legacy.NextAllowed)
		}
	}
	if a.state.NextAllowed.After(time.Now()) && (!found || a.state.NextAllowed.After(state.NextAllowed)) {
		if err := a.persist(a.state.NextAllowed); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

func (a *historyAdmission) finishStopped() {
	a.releaseAccount()
	a.joinedOnce.Do(func() { close(a.stoppedDone) })
}
