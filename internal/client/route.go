package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/booking"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// This file holds the connect-time routing state that the official Mac client
// keeps between logins (research/bridge/CONNECT-TRAFFIC.md, finding #6): the
// persisted GETCONF reply, the cached CHECKIN carriage route, and the ticket
// address cursor.

// bookingStore persists the latest accepted GETCONF reply.
type bookingStore interface {
	load() ([]byte, bool)
	save(reply []byte) error
}

// storedBooking is the profile's GETCONF file: the reply body as received.
type storedBooking struct {
	Version int    `json:"version"`
	Reply   []byte `json:"reply"`
}

const storedBookingVersion = 1

// fileBookingStore keeps the GETCONF reply beside the profile's state file.
type fileBookingStore struct{ path string }

// load returns the stored reply. A missing, unreadable or undecodable file
// counts as no stored configuration, as the Mac client's revision 0 does.
func (s fileBookingStore) load() ([]byte, bool) {
	stored, ok, err := privatejson.Read[storedBooking](s.path)
	if err != nil || !ok || stored.Version != storedBookingVersion {
		return nil, false
	}
	if _, _, err := booking.DecodeBookingTargets(stored.Reply); err != nil {
		return nil, false
	}
	return stored.Reply, true
}

func (s fileBookingStore) save(reply []byte) error {
	return privatejson.WriteAtomic(s.path, storedBooking{Version: storedBookingVersion, Reply: reply})
}

// carriageRoute is one profile's in-memory routing state. Like the Mac
// client's memory, it lasts for the process; every Client opened for the
// same profile shares it.
type carriageRoute struct {
	mu   sync.Mutex
	host string
	port int
	// expire is the CHECKIN reply's cacheExpire, and checkedAt the
	// monotonic time of that CHECKIN; zero checkedAt means no route.
	expire    time.Duration
	checkedAt time.Time
	// cursor is the ticket address that the next CHECKIN walk starts at,
	// in a pool of addresses entries. It stays on the address that last
	// served CHECKIN.
	cursor    int
	addresses int
}

var (
	routesMu sync.Mutex
	routes   = map[string]*carriageRoute{}
)

// routeFor returns the shared routing state of the profile at statePath.
func routeFor(statePath string) *carriageRoute {
	routesMu.Lock()
	defer routesMu.Unlock()
	route := routes[statePath]
	if route == nil {
		route = &carriageRoute{}
		routes[statePath] = route
	}
	return route
}

// cached returns the cached route while it is valid at now. A route expires
// once now - checkedAt reaches its cacheExpire.
func (r *carriageRoute) cached(now time.Time) (string, int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.checkedAt.IsZero() || r.expire <= 0 || now.Sub(r.checkedAt) >= r.expire {
		return "", 0, false
	}
	return r.host, r.port, true
}

// store records the route that CHECKIN returned at now from ticket address
// cursor.
func (r *carriageRoute) store(host string, port int, expire time.Duration, now time.Time, cursor int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host, r.port, r.expire, r.checkedAt, r.cursor = host, port, expire, now, cursor
}

// clear drops the cached route.
func (r *carriageRoute) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host, r.port, r.expire, r.checkedAt = "", 0, 0, time.Time{}
}

// changeServer handles CHANGESVR: it drops the cached route and moves the
// ticket cursor to the next address when one remains.
func (r *carriageRoute) changeServer() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.host, r.port, r.expire, r.checkedAt = "", 0, 0, time.Time{}
	if r.cursor+1 < r.addresses {
		r.cursor++
	}
}

func (r *carriageRoute) resetCursor() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cursor = 0
}

// walkStart records the pool about to be walked and returns the address the
// walk starts at.
func (r *carriageRoute) walkStart(addresses int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cursor >= addresses {
		r.cursor = 0
	}
	r.addresses = addresses
	return r.cursor
}

// routeClock is the time and randomness source of connect-time routing.
type routeClock struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	// intN returns a uniform integer in [0, n).
	intN func(n int) int
}

func realtimeRouteClock() routeClock {
	return routeClock{now: time.Now, sleep: sleepContext, intN: rand.IntN}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GETCONF makes at most three attempts in all; ticket attempts move through
// the address pool. Both wait jitteredBackoff with these caps.
const (
	bookingAttempts   = 3
	bookingBackoffCap = 8 * time.Second
	ticketBackoffCap  = 32 * time.Second
)

// jitteredBackoff is the Mac client's equal-jitter delay before attempt n:
// half = min(2^n / 2, cap) / 2 seconds, plus a uniform whole number of
// seconds in [0, floor(half)].
func jitteredBackoff(n int, limit time.Duration, intN func(int) int) time.Duration {
	if n < 1 {
		return 0
	}
	base := limit
	if n < 32 {
		base = min(time.Duration(1<<n)*time.Second/2, limit)
	}
	half := base / 2
	return half + time.Duration(intN(int(half/time.Second)+1))*time.Second
}

// ticketAddress is one CHECKIN endpoint, always dialed over TLS.
type ticketAddress struct {
	host string
	port int
}

// ticketPool lists the CHECKIN addresses in the Mac client's order: for each
// GETCONF wifi port, every ticket host. The Mac client adds no other ports
// and has no secure-transport fallback for CHECKIN.
func ticketPool(hosts []string, ports []int) []ticketAddress {
	var pool []ticketAddress
	for _, port := range ports {
		for _, host := range hosts {
			pool = append(pool, ticketAddress{host: host, port: port})
		}
	}
	return pool
}

// errTicketPoolExhausted marks a CHECKIN walk that reached the end of the
// pool without an accepted reply.
var errTicketPoolExhausted = errors.New("client: every ticket address failed")

// checkin walks the ticket pool from start to its end, as the Mac client
// does. The first address is tried at once; the address at index n waits
// jitteredBackoff(n, 32 s) first. It returns the accepted reply and the index
// that served it. When the walk reaches the end it reports the last failure,
// tagged "checkin dial" or "checkin", joined with errTicketPoolExhausted.
func checkin(ctx context.Context, pool []ticketAddress, start int, body []byte, dialers sessionDialers, clock routeClock) (loco.Packet, int, error) {
	failure := errBootstrap
	if start < 0 || start >= len(pool) {
		start = 0
	}
	for index := start; index < len(pool); index++ {
		if index > start {
			if err := clock.sleep(ctx, jitteredBackoff(index, ticketBackoffCap, clock.intN)); err != nil {
				return loco.Packet{}, 0, stageErr(errBootstrap, "checkin", err)
			}
		}
		address := pool[index]
		wire, err := dialers.tls(ctx, address.host, address.port)
		if err != nil {
			failure = stageErr(errBootstrap, "checkin dial", err)
			continue
		}
		reply, _, requestErr := wire.request(1, booking.CheckinCommand, body)
		_ = wire.close()
		status, statusErr := loco.Status(reply)
		if cause := replyFailure(booking.CheckinCommand, requestErr, statusErr, status); cause != nil {
			failure = stageErr(errBootstrap, "checkin", cause)
			continue
		}
		return reply, index, nil
	}
	return loco.Packet{}, 0, errors.Join(failure, errTicketPoolExhausted)
}

// getConf books: it sends GETCONF, retrying at most bookingAttempts times in
// all with jitteredBackoff(n, 8 s), and persists the accepted reply.
func getConf(ctx context.Context, body []byte, dialers sessionDialers, store bookingStore, clock routeClock) ([]byte, error) {
	var failure error
	for n := range bookingAttempts {
		if n > 0 {
			if err := clock.sleep(ctx, jitteredBackoff(n, bookingBackoffCap, clock.intN)); err != nil {
				return nil, stageErr(errBootstrap, "getconf", err)
			}
		}
		var reply []byte
		reply, failure = getConfOnce(ctx, body, dialers)
		if failure != nil {
			continue
		}
		if store != nil {
			// A failed save only costs a GETCONF on the next connect.
			_ = store.save(reply)
		}
		return reply, nil
	}
	return nil, failure
}

func getConfOnce(ctx context.Context, body []byte, dialers sessionDialers) ([]byte, error) {
	wire, err := dialers.tls(ctx, booking.Host, 443)
	if err != nil {
		return nil, stageErr(errBootstrap, "getconf dial", err)
	}
	reply, _, err := wire.request(1, booking.GetConfCommand, body)
	_ = wire.close()
	status, statusErr := loco.Status(reply)
	if cause := replyFailure(booking.GetConfCommand, err, statusErr, status); cause != nil {
		return nil, stageErr(errBootstrap, "getconf", cause)
	}
	if _, _, err := booking.DecodeBookingTargets(reply.Body); err != nil {
		return nil, stageErr(errBootstrap, "getconf", err)
	}
	return reply.Body, nil
}

// dialCarriage opens the secure carriage connection. A valid cached route
// is dialed without CHECKIN. If that dial fails, the route is dropped and
// the same attempt falls through to CHECKIN (a deliberate deviation: the Mac
// client keeps its in-memory route until it expires, which would retry a
// dead host for up to an hour). A route from CHECKIN is cached for its
// cacheExpire.
func dialCarriage(ctx context.Context, route *carriageRoute, pool []ticketAddress, checkinBody []byte, dialers sessionDialers, clock routeClock) (*wireConn, error) {
	if host, port, ok := route.cached(clock.now()); ok {
		carriage, err := dialers.secure(ctx, host, port)
		if err == nil {
			return carriage, nil
		}
		route.clear()
	}
	reply, index, err := checkin(ctx, pool, route.walkStart(len(pool)), checkinBody, dialers, clock)
	if err != nil {
		// An exhausted walk starts again at the first address.
		route.resetCursor()
		return nil, err
	}
	host, port, err := booking.DecodeCheckinEndpoint(reply.Body)
	if err != nil {
		return nil, stageErr(errBootstrap, "checkin", err)
	}
	route.store(host, port, booking.DecodeCheckinCacheExpire(reply.Body), clock.now(), index)
	carriage, err := dialers.secure(ctx, host, port)
	if err != nil {
		route.clear()
		return nil, stageErr(errBootstrap, "carriage dial", err)
	}
	return carriage, nil
}
