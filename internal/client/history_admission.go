package client

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// HistoryPacing is operator policy, not a claimed Kakao server rate limit.
// Zero values select conservative defaults. It applies to zero-held-count
// SYNCMSG acquisition; explicit read acknowledgements have a separate path.
type HistoryPacing struct {
	MinimumInterval time.Duration `yaml:"minimum_interval"`
	ErrorBackoff    time.Duration `yaml:"error_backoff"`
	MaxPending      int           `yaml:"max_pending"`
	MaximumDuration time.Duration `yaml:"maximum_duration"`
}

func (p HistoryPacing) normalized() (HistoryPacing, error) {
	if p.MaximumDuration == 0 {
		p.MaximumDuration = time.Minute
	}
	if p.MaxPending == 0 {
		p.MaxPending = 32
	}
	if p.MinimumInterval == 0 {
		p.MinimumInterval = 2 * time.Second
	}
	if p.ErrorBackoff == 0 {
		p.ErrorBackoff = 30 * time.Second
	}
	if p.MinimumInterval < 0 || p.ErrorBackoff < p.MinimumInterval || p.ErrorBackoff > 24*time.Hour || p.MaxPending < 1 || p.MaxPending > 4096 || p.MaximumDuration < 0 || p.MaximumDuration > 24*time.Hour {
		return HistoryPacing{}, ErrProtocol
	}
	return p, nil
}

var ErrHistoryBusy = errors.New("client: history demand queue is full")

type historyAdmissionState struct {
	Version     uint32    `json:"version"`
	AccountID   int64     `json:"account_id"`
	NextAllowed time.Time `json:"next_allowed"`
}

type historyFlight struct {
	done     chan struct{}
	response syncmsg.Response
	err      error
}

type historyAdmission struct {
	mu      sync.Mutex
	gate    chan struct{}
	flights map[syncmsg.Request]*historyFlight
	state   historyAdmissionState
	path    string
	policy  HistoryPacing
	failed  error
	pending int
	stopCtx context.Context
	stop    context.CancelFunc
}

func openHistoryAdmission(path string, accountID int64, policy HistoryPacing) (*historyAdmission, error) {
	policy, err := policy.normalized()
	if err != nil || accountID <= 0 {
		return nil, ErrProtocol
	}
	a := &historyAdmission{gate: make(chan struct{}, 1), flights: make(map[syncmsg.Request]*historyFlight), path: path, policy: policy, state: historyAdmissionState{Version: 1, AccountID: accountID}}
	a.stopCtx, a.stop = context.WithCancel(context.Background())
	if path != "" {
		if err = privatejson.ValidatePrivateDir(filepath.Dir(path)); err != nil {
			return nil, err
		}
		state, found, err := privatejson.Read[historyAdmissionState](path)
		if err != nil {
			return nil, err
		}
		if found {
			if state.Version != 1 || state.AccountID != accountID || state.NextAllowed.IsZero() || state.NextAllowed.After(time.Now().Add(24*time.Hour)) {
				return nil, privatejson.ErrCorrupt
			}
			a.state = state
		}
	}
	return a, nil
}

func cloneHistoryResponse(r syncmsg.Response) syncmsg.Response {
	out := syncmsg.Response{MinLogID: r.MinLogID, ChatLogs: make([]bson.Raw, len(r.ChatLogs))}
	for i, raw := range r.ChatLogs {
		out.ChatLogs[i] = append(bson.Raw(nil), raw...)
	}
	return out
}

func (a *historyAdmission) run(ctx context.Context, request syncmsg.Request, fetch func(context.Context) (syncmsg.Response, error)) (syncmsg.Response, error) {
	if err := ctx.Err(); err != nil {
		return syncmsg.Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.policy.MaximumDuration)
	defer cancel()
	if a.stopCtx.Err() != nil {
		return syncmsg.Response{}, ErrClientClosed
	}
	a.mu.Lock()
	if a.pending >= a.policy.MaxPending {
		a.mu.Unlock()
		return syncmsg.Response{}, ErrHistoryBusy
	}
	a.pending++
	defer func() { a.mu.Lock(); a.pending--; a.mu.Unlock() }()
	if f := a.flights[request]; f != nil {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return syncmsg.Response{}, ctx.Err()
		case <-a.stopCtx.Done():
			return syncmsg.Response{}, ErrClientClosed
		case <-f.done:
			return cloneHistoryResponse(f.response), f.err
		}
	}
	f := &historyFlight{done: make(chan struct{})}
	a.flights[request] = f
	a.mu.Unlock()
	response, err := a.dispatch(ctx, request, fetch)
	a.mu.Lock()
	f.response = cloneHistoryResponse(response)
	f.err = err
	delete(a.flights, request)
	close(f.done)
	a.mu.Unlock()
	return response, err
}

// persist is called only by the gate owner. A failed durable write poisons the
// admission object, so it cannot silently proceed with an unrecorded cooldown.
func (a *historyAdmission) persist(next time.Time) error {
	a.state.NextAllowed = next
	if a.path != "" {
		if err := privatejson.WriteAtomic(a.path, a.state); err != nil {
			a.failed = err
			return err
		}
	}
	return nil
}

func (a *historyAdmission) dispatch(ctx context.Context, request syncmsg.Request, fetch func(context.Context) (syncmsg.Response, error)) (syncmsg.Response, error) {
	select {
	case <-ctx.Done():
		return syncmsg.Response{}, ctx.Err()
	case <-a.stopCtx.Done():
		return syncmsg.Response{}, ErrClientClosed
	case a.gate <- struct{}{}:
	}
	defer func() { <-a.gate }()
	if a.failed != nil {
		return syncmsg.Response{}, a.failed
	}
	if wait := time.Until(a.state.NextAllowed); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return syncmsg.Response{}, ctx.Err()
		case <-a.stopCtx.Done():
			return syncmsg.Response{}, ErrClientClosed
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return syncmsg.Response{}, err
	}
	// Reserve the longer error cooldown before dispatch. A process crash or lost
	// response must not reset admission to the shorter successful-request spacing.
	if err := a.persist(time.Now().Add(a.policy.ErrorBackoff)); err != nil {
		return syncmsg.Response{}, err
	}
	fetchCtx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(a.stopCtx, cancel)
	defer stopCancel()
	defer cancel()
	response, err := fetch(fetchCtx)
	if err == nil {
		// Validate the entire returned page before retiring admission. Inclusive-only,
		// foreign-room and otherwise non-progressing responses get error cooldowns.
		_, err = decodeHistoryPage(request, response, max(1, len(response.ChatLogs)), nil)
	}
	if err != nil {
		if persistErr := a.persist(time.Now().Add(a.policy.ErrorBackoff)); persistErr != nil {
			err = errors.Join(err, persistErr)
		}
		return syncmsg.Response{}, err
	}
	if err = a.persist(time.Now().Add(a.policy.MinimumInterval)); err != nil {
		return syncmsg.Response{}, err
	}
	return response, nil
}
