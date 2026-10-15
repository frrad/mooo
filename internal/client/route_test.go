package client

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/booking"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// routeBackends scripts every booking, CHECKIN and carriage connection with a
// fresh backend and counts the dials per host. A host listed in dead refuses
// its dials.
type routeBackends struct {
	t  *testing.T
	mu sync.Mutex
	// dials lists every dial as "tls host:port" or "secure host:port".
	dials        []string
	dead         map[string]bool
	checkinHosts []string
	cacheExpire  int32
	carriage     string
}

func newRouteBackends(t *testing.T) *routeBackends {
	return &routeBackends{t: t, dead: map[string]bool{}, checkinHosts: []string{"checkin-a.invalid"}, cacheExpire: 3600, carriage: "carriage-a.invalid"}
}

func (b *routeBackends) dialers() sessionDialers {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic refused")}
	return sessionDialers{
		tls: func(_ context.Context, host string, port int) (*wireConn, error) {
			b.mu.Lock()
			b.dials = append(b.dials, "tls "+net.JoinHostPort(host, itoa(port)))
			dead, hosts, expire, carriage := b.dead[host], append([]string(nil), b.checkinHosts...), b.cacheExpire, b.carriage
			b.mu.Unlock()
			if dead {
				return nil, refused
			}
			if host == booking.Host {
				lsl := bson.A{}
				for _, h := range hosts {
					lsl = append(lsl, h)
				}
				return clientWire(newScriptedBackend(b.t, false, testloco.Expect("GETCONF", nil, statusDocument(
					bson.E{Key: "revision", Value: int32(200)},
					bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: lsl}}},
					bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
				)))), nil
			}
			return clientWire(newScriptedBackend(b.t, false, testloco.Expect("CHECKIN", nil, statusDocument(
				bson.E{Key: "host", Value: carriage}, bson.E{Key: "port", Value: int32(995)},
				bson.E{Key: "cacheExpire", Value: expire},
			)))), nil
		},
		secure: func(_ context.Context, host string, port int) (*wireConn, error) {
			b.mu.Lock()
			b.dials = append(b.dials, "secure "+net.JoinHostPort(host, itoa(port)))
			dead := b.dead[host]
			b.mu.Unlock()
			if dead {
				return nil, refused
			}
			return clientWire(newScriptedBackend(b.t, true,
				testloco.Expect("LOGINLIST", nil, statusDocument(bson.E{Key: "chatDatas", Value: bson.A{}}, bson.E{Key: "eof", Value: true})),
				testloco.HoldOpen(),
			)), nil
		},
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// take returns and clears the dials so far.
func (b *routeBackends) take() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	dials := b.dials
	b.dials = nil
	return dials
}

func assertDials(t *testing.T, step string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: dials = %q, want %q", step, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: dials = %q, want %q", step, got, want)
		}
	}
}

// memoryBooking is a bookingStore in memory.
type memoryBooking struct {
	reply []byte
	saves int
}

func (m *memoryBooking) load() ([]byte, bool) { return m.reply, m.reply != nil }
func (m *memoryBooking) save(reply []byte) error {
	m.reply = append([]byte(nil), reply...)
	m.saves++
	return nil
}

// testRouteClock is a routeClock whose time moves only when the test or a
// backoff moves it. intN returns its largest value, so backoffs are maximal.
type testRouteClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newTestRouteClock() *testRouteClock {
	return &testRouteClock{now: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
}

func (c *testRouteClock) clock() routeClock {
	return routeClock{
		now: func() time.Time {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.now
		},
		sleep: func(_ context.Context, d time.Duration) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.sleeps = append(c.sleeps, d)
			c.now = c.now.Add(d)
			return nil
		},
		intN: func(n int) int { return n - 1 },
	}
}

func (c *testRouteClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func connectRoute(t *testing.T, backends *routeBackends, store bookingStore, route *carriageRoute, clock *testRouteClock) error {
	t.Helper()
	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{
		resume: continuity.Checkpoint{Version: continuity.Version}, dialers: backends.dialers(),
		booking: store, route: route, clock: clock.clock(),
	})
	if err == nil {
		_ = session.Close()
	}
	return err
}

// A reconnect inside the CHECKIN cacheExpire sends neither GETCONF nor
// CHECKIN; one at or after it checks in again; a restart with an empty route
// cache reuses the stored GETCONF.
func TestConnectSessionReusesStoredGetConfAndCachedRoute(t *testing.T) {
	backends := newRouteBackends(t)
	store := &memoryBooking{}
	route := &carriageRoute{}
	clock := newTestRouteClock()

	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "first connect", backends.take(), "tls booking-loco.kakao.com:443", "tls checkin-a.invalid:443", "secure carriage-a.invalid:995")
	if store.saves != 1 {
		t.Fatalf("GETCONF saves = %d, want 1", store.saves)
	}

	clock.advance(3599 * time.Second)
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "reconnect inside cacheExpire", backends.take(), "secure carriage-a.invalid:995")

	clock.advance(time.Second)
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "reconnect at cacheExpire", backends.take(), "tls checkin-a.invalid:443", "secure carriage-a.invalid:995")

	if err := connectRoute(t, backends, store, &carriageRoute{}, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "restart with the stored GETCONF", backends.take(), "tls checkin-a.invalid:443", "secure carriage-a.invalid:995")
	if store.saves != 1 {
		t.Fatalf("GETCONF saves = %d, want 1", store.saves)
	}
}

// A CHECKIN reply without a positive cacheExpire is never reused.
func TestConnectSessionWithoutCacheExpireChecksInEveryTime(t *testing.T) {
	backends := newRouteBackends(t)
	backends.cacheExpire = 0
	store := &memoryBooking{}
	route := &carriageRoute{}
	clock := newTestRouteClock()
	for range 2 {
		if err := connectRoute(t, backends, store, route, clock); err != nil {
			t.Fatal(err)
		}
	}
	assertDials(t, "two connects", backends.take(),
		"tls booking-loco.kakao.com:443", "tls checkin-a.invalid:443", "secure carriage-a.invalid:995",
		"tls checkin-a.invalid:443", "secure carriage-a.invalid:995")
}

// A failed dial to the cached route drops it and checks in again in the same
// attempt.
func TestConnectSessionCachedRouteDialFailureFallsThroughToCheckin(t *testing.T) {
	backends := newRouteBackends(t)
	store := &memoryBooking{}
	route := &carriageRoute{}
	clock := newTestRouteClock()
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	backends.take()

	backends.dead["carriage-a.invalid"] = true
	backends.carriage = "carriage-b.invalid"
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "dead cached route", backends.take(), "secure carriage-a.invalid:995", "tls checkin-a.invalid:443", "secure carriage-b.invalid:995")
	if host, _, ok := route.cached(clock.clock().now()); !ok || host != "carriage-b.invalid" {
		t.Fatalf("cached route = %q %v, want the new carriage", host, ok)
	}
}

// CHANGESVR and KICKOUT drop the route; CHANGESVR also moves the next
// CHECKIN to the next ticket address.
func TestRouteChangeServerAndKickoutInvalidate(t *testing.T) {
	backends := newRouteBackends(t)
	backends.checkinHosts = []string{"checkin-a.invalid", "checkin-b.invalid"}
	store := &memoryBooking{}
	route := &carriageRoute{}
	clock := newTestRouteClock()
	c := &Client{route: route}
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	backends.take()

	if err := c.observeRouteChange(loco.Packet{}, events.ChangeServer{}); err != nil {
		t.Fatal(err)
	}
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	// The walk starts at once at the second ticket address.
	assertDials(t, "after CHANGESVR", backends.take(), "tls checkin-b.invalid:443", "secure carriage-a.invalid:995")
	route.mu.Lock()
	cursor := route.cursor
	route.mu.Unlock()
	if cursor != 1 {
		t.Fatalf("ticket cursor = %d, want 1", cursor)
	}

	if err := c.observeRouteChange(loco.Packet{}, events.Kickout{}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := route.cached(clock.clock().now()); ok {
		t.Fatal("KICKOUT kept the cached route")
	}
}

// The CHECKIN pool is port-major and TLS only. The walk tries its first
// address at once and waits the native equal jitter, by address index and
// capped at 32 s, before each later one. When the stored pool is exhausted
// the client books again once and walks the new pool.
func TestCheckinWalkJitterAndStalePoolRebook(t *testing.T) {
	backends := newRouteBackends(t)
	backends.checkinHosts = []string{"checkin-a.invalid", "checkin-b.invalid", "checkin-c.invalid"}
	store := &memoryBooking{}
	route := &carriageRoute{}
	clock := newTestRouteClock()
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	backends.take()
	route.clear()

	// The stored pool's hosts are all dead; a fresh GETCONF names a live one.
	for _, host := range []string{"checkin-a.invalid", "checkin-b.invalid", "checkin-c.invalid"} {
		backends.dead[host] = true
	}
	backends.checkinHosts = []string{"checkin-d.invalid"}
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "stale pool", backends.take(),
		"tls checkin-a.invalid:443", "tls checkin-b.invalid:443", "tls checkin-c.invalid:443",
		"tls booking-loco.kakao.com:443", "tls checkin-d.invalid:443", "secure carriage-a.invalid:995")
	clock.mu.Lock()
	sleeps := append([]time.Duration(nil), clock.sleeps...)
	clock.mu.Unlock()
	if len(sleeps) != 2 || sleeps[0] != 500*time.Millisecond || sleeps[1] != 2*time.Second {
		t.Fatalf("CHECKIN backoffs = %v, want [500ms 2s]", sleeps)
	}
	if store.saves != 2 {
		t.Fatalf("GETCONF saves = %d, want 2", store.saves)
	}
}

func TestTicketPoolIsPortMajor(t *testing.T) {
	got := ticketPool([]string{"h0", "h1"}, []int{5223, 443})
	want := []ticketAddress{{"h0", 5223}, {"h1", 5223}, {"h0", 443}, {"h1", 443}}
	if len(got) != len(want) {
		t.Fatalf("pool = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pool = %v, want %v", got, want)
		}
	}
}

// The walk starts at the address that last served CHECKIN and stops at the
// end of the pool; an exhausted walk starts again at the first address.
func TestCheckinWalkKeepsCursorUntilExhausted(t *testing.T) {
	backends := newRouteBackends(t)
	backends.checkinHosts = []string{"checkin-a.invalid", "checkin-b.invalid", "checkin-c.invalid"}
	backends.cacheExpire = 0
	store := &memoryBooking{}
	route := &carriageRoute{}
	clock := newTestRouteClock()
	backends.dead["checkin-a.invalid"] = true
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "first", backends.take(), "tls booking-loco.kakao.com:443", "tls checkin-a.invalid:443", "tls checkin-b.invalid:443", "secure carriage-a.invalid:995")

	backends.dead["checkin-a.invalid"] = false
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "kept cursor", backends.take(), "tls checkin-b.invalid:443", "secure carriage-a.invalid:995")

	backends.dead["checkin-b.invalid"] = true
	backends.dead["checkin-c.invalid"] = true
	backends.dead[booking.Host] = true
	if err := connectRoute(t, backends, store, route, clock); err == nil {
		t.Fatal("connect succeeded with every remaining address dead")
	}
	backends.take()
	backends.dead[booking.Host] = false
	if err := connectRoute(t, backends, store, route, clock); err != nil {
		t.Fatal(err)
	}
	assertDials(t, "after exhaustion", backends.take(), "tls checkin-a.invalid:443", "secure carriage-a.invalid:995")
}

// Booking makes at most three attempts, waiting the native equal jitter
// capped at 8 s.
func TestGetConfRetriesThreeTimes(t *testing.T) {
	backends := newRouteBackends(t)
	backends.dead[booking.Host] = true
	clock := newTestRouteClock()
	err := connectRoute(t, backends, &memoryBooking{}, &carriageRoute{}, clock)
	if !errors.Is(err, errBootstrap) {
		t.Fatalf("error = %v, want a bootstrap failure", err)
	}
	var networkErr net.Error
	if !errors.As(err, &networkErr) {
		t.Fatalf("error hides its network cause: %v", err)
	}
	assertDials(t, "booking", backends.take(), "tls booking-loco.kakao.com:443", "tls booking-loco.kakao.com:443", "tls booking-loco.kakao.com:443")
	clock.mu.Lock()
	sleeps := append([]time.Duration(nil), clock.sleeps...)
	clock.mu.Unlock()
	if len(sleeps) != 2 || sleeps[0] != 500*time.Millisecond || sleeps[1] != 2*time.Second {
		t.Fatalf("GETCONF backoffs = %v, want [500ms 2s]", sleeps)
	}
}

func TestJitteredBackoffBounds(t *testing.T) {
	for _, tc := range []struct {
		n        int
		limit    time.Duration
		min, max time.Duration
	}{
		{1, 32 * time.Second, 500 * time.Millisecond, 500 * time.Millisecond},
		{2, 32 * time.Second, time.Second, 2 * time.Second},
		{3, 32 * time.Second, 2 * time.Second, 4 * time.Second},
		{4, 32 * time.Second, 4 * time.Second, 8 * time.Second},
		{5, 32 * time.Second, 8 * time.Second, 16 * time.Second},
		{6, 32 * time.Second, 16 * time.Second, 32 * time.Second},
		{40, 32 * time.Second, 16 * time.Second, 32 * time.Second},
		{4, 8 * time.Second, 4 * time.Second, 8 * time.Second},
		{5, 8 * time.Second, 4 * time.Second, 8 * time.Second},
	} {
		low := jitteredBackoff(tc.n, tc.limit, func(int) int { return 0 })
		high := jitteredBackoff(tc.n, tc.limit, func(n int) int { return n - 1 })
		if low != tc.min || high != tc.max {
			t.Errorf("jitteredBackoff(%d, %s) in [%s, %s], want [%s, %s]", tc.n, tc.limit, low, high, tc.min, tc.max)
		}
	}
}

// A profile reopened in the same process shares its route, and its stored
// GETCONF survives a reopen: the second Client sends neither GETCONF nor
// CHECKIN.
func TestReopenedProfileReusesGetConfAndRoute(t *testing.T) {
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.json")
	store, err := authstate.Create(path, authstate.Config{
		DeviceName: "test", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCredentials(authstate.Credentials{UserID: 1, AccessToken: "token", AutoLoginMaterial: []byte(`{"refresh_token":"refresh","token_type":"bearer"}`)}); err != nil {
		t.Fatal(err)
	}
	backends := newRouteBackends(t)
	dialers := backends.dialers()
	testDialer := func(dial wireDialer) TestDialer {
		return func(ctx context.Context, host string, port int) (net.Conn, *loco.SecureV3, error) {
			wire, err := dial(ctx, host, port)
			if err != nil {
				return nil, nil, err
			}
			return wire.c, wire.secure, nil
		}
	}
	for range 2 {
		c, err := OpenWithTestDialers(path, nil, TestDialers{TLS: testDialer(dialers.tls), Secure: testDialer(dialers.secure)})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := c.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	assertDials(t, "two opens", backends.take(),
		"tls booking-loco.kakao.com:443", "tls checkin-a.invalid:443", "secure carriage-a.invalid:995",
		"secure carriage-a.invalid:995")
}
