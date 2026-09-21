package sessionlogin

import (
	"errors"
	"testing"
	"time"
)

func TestLoginListRequestValidationAndWireTypes(t *testing.T) {
	valid := LoginListRequest{
		AppVer:      "synthetic-client",
		OS:          "synthetic-os",
		Lang:        "en",
		DUUID:       "synthetic-device",
		OAuthToken:  "synthetic-access",
		NType:       int32(7),
		MCCMNC:      "synthetic-network",
		Revision:    int32(8),
		DType:       int32(9),
		PCST:        int32(10),
		RP:          []byte{1, 2},
		BG:          true,
		ChatIDs:     []int64{11, 12},
		MaxIDs:      []int64{21, 22},
		LastTokenID: int64(31),
		LBK:         int32(41),
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		edit func(*LoginListRequest)
		want error
	}{
		{"missing app version", func(r *LoginListRequest) { r.AppVer = "" }, ErrMissingRequiredValue},
		{"missing operating system", func(r *LoginListRequest) { r.OS = " " }, ErrMissingRequiredValue},
		{"missing language", func(r *LoginListRequest) { r.Lang = "" }, ErrMissingRequiredValue},
		{"missing device identity", func(r *LoginListRequest) { r.DUUID = "" }, ErrMissingRequiredValue},
		{"missing access token", func(r *LoginListRequest) { r.OAuthToken = "" }, ErrMissingRequiredValue},
		{"sKey set", func(r *LoginListRequest) { r.SKey = "unexpected" }, ErrSKeySet},
		{"chat list mismatch", func(r *LoginListRequest) { r.MaxIDs = r.MaxIDs[:1] }, ErrChatListLengthMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.edit(&request)
			if !errors.Is(request.Validate(), test.want) {
				t.Fatalf("Validate() = %v, want %v", request.Validate(), test.want)
			}
		})
	}

	emptyLists := valid
	emptyLists.ChatIDs = nil
	emptyLists.MaxIDs = []int64{}
	emptyLists.MCCMNC = ""
	if err := emptyLists.Validate(); err != nil {
		t.Fatalf("empty optional environment field or paired lists rejected: %v", err)
	}
}

func TestClassifyLoginStatus(t *testing.T) {
	tests := []struct {
		code     int32
		class    LoginStatusClass
		accepted bool
	}{
		{0, LoginStatusSuccess, true},
		{-305, LoginStatusSuccess, true},
		{-310, LoginStatusPartialSuccess, false},
		{-445, LoginStatusBlocked, false},
		{17, LoginStatusUnknown, false},
		{-999, LoginStatusUnknown, false},
	}
	for _, test := range tests {
		t.Run(statusName(test.code), func(t *testing.T) {
			classified := ClassifyLoginStatus(test.code)
			if classified.Code != test.code || classified.Class != test.class || classified.Accepted() != test.accepted {
				t.Fatalf("classified = %#v accepted=%v", classified, classified.Accepted())
			}
		})
	}
}

func TestEndpointCacheEligibilityAndMatchingInvalidation(t *testing.T) {
	cache := EndpointCache{
		Endpoint: Endpoint{Host: "synthetic-host", Port: 1234},
		CachedAt: 10 * time.Second,
		Lifetime: 30 * time.Second,
	}
	for _, test := range []struct {
		name string
		now  time.Duration
		want bool
	}{
		{"before cached", 9 * time.Second, false},
		{"at cached", 10 * time.Second, true},
		{"before expiry", 39*time.Second + 999*time.Millisecond, true},
		{"at expiry", 40 * time.Second, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := cache.Eligible(test.now); got != test.want {
				t.Fatalf("Eligible(%s) = %v, want %v", test.now, got, test.want)
			}
		})
	}
	for _, invalid := range []EndpointCache{
		{Endpoint: Endpoint{Host: "", Port: 1234}, CachedAt: 1, Lifetime: 2},
		{Endpoint: Endpoint{Host: "synthetic-host", Port: 0}, CachedAt: 1, Lifetime: 2},
		{Endpoint: Endpoint{Host: "synthetic-host", Port: 1234}, CachedAt: 1, Lifetime: 0},
	} {
		if invalid.Eligible(1) {
			t.Fatalf("invalid cache eligible: %#v", invalid)
		}
	}
	if got := InvalidateMatchingFailure(cache, Endpoint{Host: "other-host", Port: 1234}); got != cache {
		t.Fatalf("nonmatching failure changed cache: %#v", got)
	}
	if got := InvalidateMatchingFailure(cache, cache.Endpoint); got != (EndpointCache{}) {
		t.Fatalf("matching failure did not clear cache: %#v", got)
	}
}

func TestRecoveryAdmissionGates(t *testing.T) {
	tests := []struct {
		name  string
		state RecoveryState
		event BeginRecovery
		want  error
	}{
		{"stale", NewRecoveryState(true, true, true), BeginRecovery{Generation: 2}, ErrRecoveryStale},
		{"disabled", NewRecoveryState(false, true, true), BeginRecovery{Generation: 1}, ErrRecoveryDisabled},
		{"network unavailable", NewRecoveryState(true, true, false), BeginRecovery{Generation: 1}, ErrRecoveryNetwork},
		{"unauthenticated", NewRecoveryState(true, false, true), BeginRecovery{Generation: 1}, ErrRecoveryUnauthenticated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next, effects, err := ReduceRecovery(test.state, test.event)
			if !errors.Is(err, test.want) || len(effects) != 0 || next != test.state {
				t.Fatalf("next=%#v effects=%v err=%v, want unchanged and %v", next, effects, err, test.want)
			}
		})
	}
	running, _ := reduceRecoveryOK(t, NewRecoveryState(true, true, true), BeginRecovery{Generation: 1})
	next, effects, err := ReduceRecovery(running, BeginRecovery{Generation: 1})
	if !errors.Is(err, ErrRecoveryConcurrent) || len(effects) != 0 || next != running {
		t.Fatalf("concurrent recovery: next=%#v effects=%v err=%v", next, effects, err)
	}
}

func TestRecoverySuccessFailureAndGenerationHandling(t *testing.T) {
	s, effects := reduceRecoveryOK(t, NewRecoveryState(true, true, true), BeginRecovery{Generation: 1})
	if s.Phase != RecoveryRunning || len(effects) != 1 || effects[0].Kind != EffectBeginRecovery {
		t.Fatalf("begin = state %#v effects=%v", s, effects)
	}
	s, effects = reduceRecoveryOK(t, s, RecoverySucceeded{Generation: 1})
	if s.Phase != RecoveryCompleted || effects[0].Kind != EffectInstallSession || s.Generation != 2 || effects[0].Generation != 2 {
		t.Fatalf("success = state %#v effects=%v", s, effects)
	}

	s, _ = reduceRecoveryOK(t, NewRecoveryState(true, true, true), BeginRecovery{Generation: 1})
	s, effects = reduceRecoveryOK(t, s, RecoveryFailed{Generation: 1})
	if s.Phase != RecoveryIdle || s.Generation != 2 || len(effects) != 1 || effects[0].Kind != EffectScheduleRecovery || effects[0].Generation != 2 {
		t.Fatalf("failure = state %#v effects=%v", s, effects)
	}
	next, effects, err := ReduceRecovery(s, RecoverySucceeded{Generation: 1})
	if !errors.Is(err, ErrRecoveryStale) || len(effects) != 0 || next != s {
		t.Fatalf("stale completion: next=%#v effects=%v err=%v", next, effects, err)
	}
	max := RecoveryState{Generation: ^uint64(0), Phase: RecoveryRunning}
	next, effects, err = ReduceRecovery(max, RecoveryFailed{Generation: ^uint64(0)})
	if !errors.Is(err, ErrRecoveryOverflow) || len(effects) != 0 || next != max {
		t.Fatalf("generation overflow: next=%#v effects=%v err=%v", next, effects, err)
	}
	next, effects, err = ReduceRecovery(max, RecoverySucceeded{Generation: ^uint64(0)})
	if !errors.Is(err, ErrRecoveryOverflow) || len(effects) != 0 || next != max {
		t.Fatalf("success generation overflow: next=%#v effects=%v err=%v", next, effects, err)
	}
}

func TestRecoveryTerminalActionsRemainDistinct(t *testing.T) {
	changeState, effects := reduceRecoveryOK(t, NewRecoveryState(true, true, true), ChangeServer{Generation: 1})
	if changeState.Phase != RecoveryTerminal || changeState.Terminal != TerminalChangeServer || len(effects) != 2 || effects[0].Kind != EffectClearRoute || effects[1].Kind != EffectChangeServerLogout {
		t.Fatalf("CHANGESVR = state %#v effects=%v", changeState, effects)
	}
	kickout, effects := reduceRecoveryOK(t, NewRecoveryState(true, true, true), Kickout{Generation: 1, Reason: 7})
	if kickout.Phase != RecoveryTerminal || kickout.Terminal != TerminalKickout || len(effects) != 1 || effects[0].Kind != EffectLogout {
		t.Fatalf("KICKOUT = state %#v effects=%v", kickout, effects)
	}
	reset, effects := reduceRecoveryOK(t, NewRecoveryState(true, true, true), Kickout{Generation: 1, Reason: 10})
	if reset.KickoutReason != 10 || len(effects) != 2 || effects[0].Kind != EffectLogout || effects[1].Kind != EffectResetDatabase {
		t.Fatalf("reset KICKOUT = state %#v effects=%v", reset, effects)
	}
}

type unknownRecoveryEvent struct{}

func (unknownRecoveryEvent) recoveryEvent() {}

func TestRecoveryApplyNilAndUnknown(t *testing.T) {
	s := NewRecoveryState(true, true, true)
	next, effects, err := ReduceRecovery(s, nil)
	if !errors.Is(err, ErrRecoveryEvent) || len(effects) != 0 || next != s {
		t.Fatalf("nil recovery event: next=%#v effects=%v err=%v", next, effects, err)
	}
	next, effects, err = ReduceRecovery(s, unknownRecoveryEvent{})
	if !errors.Is(err, ErrRecoveryEvent) || len(effects) != 0 || next != s {
		t.Fatalf("unknown recovery event: next=%#v effects=%v err=%v", next, effects, err)
	}
	next, effects, err = s.Apply(BeginRecovery{Generation: 1})
	if err != nil || next.Phase != RecoveryRunning || len(effects) != 1 {
		t.Fatalf("Apply: next=%#v effects=%v err=%v", next, effects, err)
	}
}

func reduceRecoveryOK(t *testing.T, state RecoveryState, event RecoveryEvent) (RecoveryState, []RecoveryEffect) {
	t.Helper()
	next, effects, err := ReduceRecovery(state, event)
	if err != nil {
		t.Fatalf("ReduceRecovery(%T): %v", event, err)
	}
	return next, effects
}

func statusName(code int32) string {
	if code < 0 {
		return "negative"
	}
	return "status"
}
