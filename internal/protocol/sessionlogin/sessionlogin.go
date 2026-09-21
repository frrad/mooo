// Package sessionlogin contains the reviewed, transport-independent subset of
// final session login and reconnect behavior. It does not encode BSON, send
// requests, open sockets, or store credentials.
package sessionlogin

import (
	"errors"
	"strings"
	"time"
)

// LoginListRequest is the semantic LOGINLIST request model. Field types match
// the reviewed wire types; serialization and unset-object behavior remain a
// separate compatibility concern.
type LoginListRequest struct {
	AppVer      string
	OS          string
	Lang        string
	DUUID       string
	SKey        string
	OAuthToken  string
	NType       int32
	MCCMNC      string
	Revision    int32
	DType       int32
	PCST        int32
	RP          []byte
	BG          bool
	ChatIDs     []int64
	MaxIDs      []int64
	LastTokenID int64
	LBK         int32
}

var (
	ErrMissingRequiredValue    = errors.New("sessionlogin: missing required LOGINLIST value")
	ErrChatListLengthMismatch  = errors.New("sessionlogin: chatIds and maxIds lengths differ")
	ErrSKeySet                 = errors.New("sessionlogin: sKey must be unset")
	ErrRecoveryStale           = errors.New("sessionlogin: stale recovery generation")
	ErrRecoveryConcurrent      = errors.New("sessionlogin: recovery already active")
	ErrRecoveryDisabled        = errors.New("sessionlogin: recovery disabled")
	ErrRecoveryNetwork         = errors.New("sessionlogin: network unavailable")
	ErrRecoveryUnauthenticated = errors.New("sessionlogin: authentication unavailable")
	ErrRecoveryTransition      = errors.New("sessionlogin: invalid recovery transition")
	ErrRecoveryOverflow        = errors.New("sessionlogin: recovery generation overflow")
	ErrRecoveryEvent           = errors.New("sessionlogin: invalid recovery event")
)

// Validate enforces only reviewed semantic preconditions. It never inspects
// or transforms the access-token string.
func (r LoginListRequest) Validate() error {
	for _, value := range []string{r.AppVer, r.OS, r.Lang, r.DUUID, r.OAuthToken} {
		if strings.TrimSpace(value) == "" {
			return ErrMissingRequiredValue
		}
	}
	if r.SKey != "" {
		return ErrSKeySet
	}
	if len(r.ChatIDs) != len(r.MaxIDs) {
		return ErrChatListLengthMismatch
	}
	return nil
}

// LoginStatusClass is the fail-closed semantic classification of a LOGINLIST
// response status.
type LoginStatusClass uint8

const (
	LoginStatusUnknown LoginStatusClass = iota
	LoginStatusSuccess
	LoginStatusPartialSuccess
	LoginStatusBlocked
)

// ClassifiedStatus preserves the numeric status even when its meaning is
// unknown.
type ClassifiedStatus struct {
	Code  int32
	Class LoginStatusClass
}

// ClassifyLoginStatus classifies only statuses established by the public
// specification. Unknown values remain non-successful and retain their code.
func ClassifyLoginStatus(code int32) ClassifiedStatus {
	result := ClassifiedStatus{Code: code, Class: LoginStatusUnknown}
	switch code {
	case 0, -305:
		result.Class = LoginStatusSuccess
	case -310:
		result.Class = LoginStatusPartialSuccess
	case -445:
		result.Class = LoginStatusBlocked
	}
	return result
}

// Accepted reports whether the status is one of the two accepted login
// success statuses.
func (s ClassifiedStatus) Accepted() bool { return s.Class == LoginStatusSuccess }

// Endpoint is a cached carriage address. Host matching is exact and the port
// is kept as the reviewed int32 configuration type.
type Endpoint struct {
	Host string
	Port int32
}

func (e Endpoint) valid() bool { return strings.TrimSpace(e.Host) != "" && e.Port > 0 }

// EndpointCache records when an endpoint was observed using elapsed system
// uptime. It is data only; callers provide the current uptime to eligibility
// checks.
type EndpointCache struct {
	Endpoint Endpoint
	CachedAt time.Duration
	Lifetime time.Duration
}

// Eligible reports whether the cached endpoint is valid and unexpired. Equal
// age and lifetime is expired; a clock moving backwards fails closed.
func (c EndpointCache) Eligible(now time.Duration) bool {
	if !c.Endpoint.valid() || c.Lifetime <= 0 || now < c.CachedAt {
		return false
	}
	return now-c.CachedAt < c.Lifetime
}

// CacheEligible is the functional form of EndpointCache.Eligible.
func CacheEligible(cache EndpointCache, now time.Duration) bool { return cache.Eligible(now) }

// InvalidateMatchingFailure clears a cache only when the failed endpoint is
// exactly the currently cached endpoint. A failure for another address leaves
// the cache unchanged.
func InvalidateMatchingFailure(cache EndpointCache, failed Endpoint) EndpointCache {
	if cache.Endpoint == failed {
		return EndpointCache{}
	}
	return cache
}

// RecoveryPhase is the pure reconnect manager lifecycle.
type RecoveryPhase uint8

const (
	RecoveryIdle RecoveryPhase = iota
	RecoveryRunning
	RecoveryCompleted
	RecoveryTerminal
)

// TerminalAction identifies why recovery ended. CHANGESVR and KICKOUT remain
// distinct because only KICKOUT logs out and may request a database reset.
type TerminalAction uint8

const (
	TerminalNone TerminalAction = iota
	TerminalChangeServer
	TerminalKickout
)

// RecoveryState is the reducer state. Generation is supplied by the caller to
// bind asynchronous callbacks to the admitted recovery attempt.
type RecoveryState struct {
	Generation       uint64
	Phase            RecoveryPhase
	Enabled          bool
	Authenticated    bool
	NetworkReachable bool
	Terminal         TerminalAction
	KickoutReason    int32
}

// NewRecoveryState creates an idle manager with generation one.
func NewRecoveryState(enabled, authenticated, networkReachable bool) RecoveryState {
	return RecoveryState{
		Generation:       1,
		Phase:            RecoveryIdle,
		Enabled:          enabled,
		Authenticated:    authenticated,
		NetworkReachable: networkReachable,
	}
}

// RecoveryEvent is a semantic input to ReduceRecovery.
type RecoveryEvent interface{ recoveryEvent() }

// BeginRecovery admits a recovery attempt for an exact generation.
type BeginRecovery struct{ Generation uint64 }

func (BeginRecovery) recoveryEvent() {}

// RecoverySucceeded completes an admitted recovery attempt.
type RecoverySucceeded struct{ Generation uint64 }

func (RecoverySucceeded) recoveryEvent() {}

// RecoveryFailed rejects an admitted attempt and advances the generation for
// a bounded caller-scheduled retry.
type RecoveryFailed struct{ Generation uint64 }

func (RecoveryFailed) recoveryEvent() {}

// ChangeServer is the terminal CHANGESVR action.
type ChangeServer struct{ Generation uint64 }

func (ChangeServer) recoveryEvent() {}

// Kickout is the terminal KICKOUT action. Reasons 1 and 10 request local
// database reset according to the reviewed behavior; their human meanings are
// intentionally not inferred here.
type Kickout struct {
	Generation uint64
	Reason     int32
}

func (Kickout) recoveryEvent() {}

// RecoveryEffectKind identifies caller work requested by the reducer.
type RecoveryEffectKind uint8

const (
	EffectBeginRecovery RecoveryEffectKind = iota + 1
	EffectScheduleRecovery
	EffectInstallSession
	EffectClearRoute
	EffectChangeServerLogout
	EffectLogout
	EffectResetDatabase
)

// RecoveryEffect is a transport/UI/storage-neutral request. The reducer does
// not execute any effect.
type RecoveryEffect struct {
	Kind       RecoveryEffectKind
	Generation uint64
	Reason     int32
}

// ReduceRecovery applies one deterministic recovery event.
func ReduceRecovery(s RecoveryState, event RecoveryEvent) (RecoveryState, []RecoveryEffect, error) {
	if event == nil {
		return s, nil, ErrRecoveryEvent
	}
	switch e := event.(type) {
	case BeginRecovery:
		if e.Generation == 0 || e.Generation != s.Generation {
			return s, nil, ErrRecoveryStale
		}
		if s.Phase == RecoveryRunning {
			return s, nil, ErrRecoveryConcurrent
		}
		if s.Phase != RecoveryIdle {
			return s, nil, ErrRecoveryTransition
		}
		if !s.Enabled {
			return s, nil, ErrRecoveryDisabled
		}
		if !s.NetworkReachable {
			return s, nil, ErrRecoveryNetwork
		}
		if !s.Authenticated {
			return s, nil, ErrRecoveryUnauthenticated
		}
		s.Phase = RecoveryRunning
		return s, []RecoveryEffect{{Kind: EffectBeginRecovery, Generation: s.Generation}}, nil
	case RecoverySucceeded:
		if err := recoveryGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Phase != RecoveryRunning {
			return s, nil, ErrRecoveryTransition
		}
		if !advanceRecoveryGeneration(&s) {
			return s, nil, ErrRecoveryOverflow
		}
		s.Phase = RecoveryCompleted
		return s, []RecoveryEffect{{Kind: EffectInstallSession, Generation: s.Generation}}, nil
	case RecoveryFailed:
		if err := recoveryGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Phase != RecoveryRunning {
			return s, nil, ErrRecoveryTransition
		}
		if !advanceRecoveryGeneration(&s) {
			return s, nil, ErrRecoveryOverflow
		}
		s.Phase = RecoveryIdle
		return s, []RecoveryEffect{{Kind: EffectScheduleRecovery, Generation: s.Generation}}, nil
	case ChangeServer:
		if err := recoveryGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Phase == RecoveryTerminal {
			return s, nil, ErrRecoveryTransition
		}
		s.Phase = RecoveryTerminal
		s.Terminal = TerminalChangeServer
		return s, []RecoveryEffect{
			{Kind: EffectClearRoute, Generation: s.Generation},
			{Kind: EffectChangeServerLogout, Generation: s.Generation},
		}, nil
	case Kickout:
		if err := recoveryGeneration(s, e.Generation); err != nil {
			return s, nil, err
		}
		if s.Phase == RecoveryTerminal {
			return s, nil, ErrRecoveryTransition
		}
		s.Phase = RecoveryTerminal
		s.Terminal = TerminalKickout
		s.KickoutReason = e.Reason
		effects := []RecoveryEffect{{Kind: EffectLogout, Generation: s.Generation, Reason: e.Reason}}
		if e.Reason == 1 || e.Reason == 10 {
			effects = append(effects, RecoveryEffect{Kind: EffectResetDatabase, Generation: s.Generation, Reason: e.Reason})
		}
		return s, effects, nil
	default:
		return s, nil, ErrRecoveryEvent
	}
}

// Apply is the method form of ReduceRecovery.
func (s RecoveryState) Apply(event RecoveryEvent) (RecoveryState, []RecoveryEffect, error) {
	return ReduceRecovery(s, event)
}

func recoveryGeneration(s RecoveryState, generation uint64) error {
	if generation == 0 || generation != s.Generation {
		return ErrRecoveryStale
	}
	return nil
}

func advanceRecoveryGeneration(s *RecoveryState) bool {
	if s.Generation == ^uint64(0) {
		return false
	}
	s.Generation++
	return s.Generation != 0
}
