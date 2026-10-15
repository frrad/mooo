package connector

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
)

var terminalDisconnectTimeout = 5 * time.Second

const recoveryCleanupTimeout = 5 * time.Second

// connLifecycle is a KakaoClient's connection lifecycle state: the active
// session, the retained profile owner, and the background workers that
// recover or clean them up. mu guards every field.
type connLifecycle struct {
	mu sync.Mutex
	// client is the connected session; done closes when its event pump exits.
	client kakaoClient
	done   chan struct{}
	// cleanup is the profile owner. It is retained from bootstrap until a
	// Shutdown succeeds, and no replacement session opens while it is set.
	cleanup  kakaoClient
	retained ownerCleanup
	// generation identifies the current Connect..Disconnect attempt; work
	// started for an older generation must not change state.
	generation           uint64
	connecting           bool
	connectingGeneration uint64
	connectCancel        context.CancelFunc
	stopping             bool
	// lifecycle spans one Connect..Disconnect cycle, including bounded
	// recovery reconnects. Disconnect cancels it before waiting for the event
	// pump, which interrupts in-flight inbound conversions.
	lifecycle       context.Context
	lifecycleCancel context.CancelFunc
	// retry waits out a recovery backoff and then reconnects; schedule is
	// the recovery episode that chooses the backoff, and watchdog its armed
	// 600 s timer.
	retry         worker
	schedule      recoverySchedule
	watchdog      stopper
	watchdogAfter func(time.Duration, func()) stopper
	// connectedAt is when the current session connected.
	connectedAt time.Time
	// cleanupRetry is the single delayed retry of a timed-out owner cleanup;
	// cleanupRetryAttempts bounds it per connected session.
	cleanupRetry         worker
	cleanupRetryAttempts int
	// wait sleeps for a recovery or cleanup delay and now reads the clock;
	// tests replace both.
	wait func(context.Context, time.Duration) error
	now  func() time.Time
}

func (kc *KakaoClient) Connect(ctx context.Context) {
	kc.mu.Lock()
	if kc.client != nil || kc.cleanup != nil || kc.connecting {
		kc.mu.Unlock()
		return
	}
	if record, kicked := kc.kickedOut(); kicked {
		kc.mu.Unlock()
		// The source signed this device out. Reconnecting with the same
		// credentials after a restart could displace whichever device caused
		// the kick, so a new login is required.
		kc.sendState(kickedOutState(record))
		return
	}
	kc.stopping = false
	if kc.lifecycle == nil {
		kc.lifecycle, kc.lifecycleCancel = context.WithCancel(context.Background())
	}
	kc.generation++
	generation := kc.generation
	// An explicit Connect (the bridge starting, or the operator's reconnect
	// command) starts a fresh episode, as the native client does after a
	// restart or a new login. No schedule survives the process.
	kc.stopWatchdogLocked()
	kc.schedule = recoverySchedule{}
	kc.connecting = true
	kc.connectingGeneration = generation
	connectCtx, cancel := context.WithCancel(ctx)
	kc.connectCancel = cancel
	kc.mu.Unlock()
	defer func() {
		cancel()
		kc.mu.Lock()
		if kc.generation == generation || (kc.connecting && kc.connectingGeneration == generation) {
			kc.connecting = false
			kc.connectCancel = nil
		}
		kc.mu.Unlock()
	}()

	kc.sendState(status.BridgeState{StateEvent: status.StateConnecting})
	kc.connectOnce(connectCtx, generation, false)
}

func (kc *KakaoClient) connectOnce(ctx context.Context, generation uint64, recovering bool) {
	// report publishes a bootstrap state only while this attempt is current;
	// a superseded or stopped attempt must not overwrite a newer state.
	report := func(state status.BridgeState) {
		if kc.isCurrent(generation, ctx) {
			kc.sendState(state)
		}
	}
	c, err := kc.open()
	if err != nil {
		kc.log().Err(err).Msg("Failed to open Kakao profile")
		if !recovering {
			report(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateProfileUnavailable})
		} else {
			report(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateConnectFailed})
		}
		return
	}
	if !kc.isCurrent(generation, ctx) {
		kc.mu.Lock()
		if kc.cleanup == nil {
			kc.retain(c)
		}
		kc.mu.Unlock()
		kc.shutdownBootstrap(c, "stale bootstrap", true)
		return
	}
	// Retain the bootstrap owner before any potentially blocking Connect,
	// catch-up, or Events call. Disconnect must be able to interrupt and join a
	// client whose subscription has not returned yet.
	kc.mu.Lock()
	kc.retain(c)
	if now := kc.now(); kc.schedule.begin(now) {
		kc.armWatchdogLocked(generation, kc.schedule.watchdogDelay(now))
	}
	kc.mu.Unlock()
	stream, err := kc.connectAndSubscribe(ctx, c)
	if err != nil {
		kc.shutdownBootstrap(c, "after connect failure", false)
		kc.log().Err(err).Msg("Failed to connect to KakaoTalk")
		switch {
		case errors.Is(err, errSourceAccessRemoved):
			report(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupAccessRemoved})
		case errors.Is(err, errMembershipPending):
			report(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupMembershipPending})
		case errors.Is(err, errGroupCreateUnresolved):
			report(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupCreateUnresolved})
		case isUnidentifiableMessageError(err):
			report(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateUnidentifiableMsg})
		case kc.retryAfter(err, generation):
			// The first connect retries too: a network outage at startup must
			// not leave the bridge down until a restart.
			if errors.Is(err, errDeliveryNotConfirmed) {
				report(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDeliveryPaused})
			}
		default:
			report(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateConnectFailed})
		}
		if !retryableRecoveryError(err) {
			// Recovery stops here and waits for the operator, as after the
			// native logout, so the episode ends.
			kc.endEpisode(generation)
		}
		return
	}
	done := make(chan struct{})
	kc.mu.Lock()
	if kc.stopping || kc.generation != generation || kc.cleanup != c {
		kc.mu.Unlock()
		kc.shutdownBootstrap(c, "stopped client", true)
		return
	}
	kc.client = c
	if kc.reactionView != nil && kc.reactionView.cancel != nil {
		kc.reactionView.cancel()
	}
	kc.reactionView = nil
	kc.done = done
	kc.connectedAt = kc.now()
	if c.LoginCompleted() {
		kc.schedule.succeeded()
		kc.stopWatchdogLocked()
	}
	kc.cleanupRetryAttempts = 0
	kc.mu.Unlock()
	kc.sendReadyState()
	go kc.run(c, stream, done, generation)
}

// connectionLifecycle returns the current connection lifecycle, or nil when
// the client is not between Connect and Disconnect.
func (kc *KakaoClient) connectionLifecycle() context.Context {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	return kc.lifecycle
}

func (kc *KakaoClient) isCurrent(generation uint64, ctx context.Context) bool {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	return kc.generation == generation && !kc.stopping && ctx.Err() == nil
}

func waitForRecovery(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (kc *KakaoClient) retryAfter(err error, generation uint64) bool {
	if !retryableRecoveryError(err) {
		return false
	}
	return kc.scheduleRecovery(generation, func(schedule *recoverySchedule, now time.Time) (time.Duration, bool) {
		return schedule.failed(now)
	})
}

// scheduleRecovery starts the next recovery attempt for generation after the
// delay that next chooses from the schedule. When next declines, no attempt
// is scheduled and the episode's watchdog ends it. It reports false when the
// generation is no longer current.
func (kc *KakaoClient) scheduleRecovery(generation uint64, next func(*recoverySchedule, time.Time) (time.Duration, bool)) bool {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.stopping || kc.generation != generation {
		return false
	}
	if delay, ok := next(&kc.schedule, kc.now()); ok {
		kc.startRetryLocked(generation, delay)
	}
	return true
}

// armWatchdogLocked arms the episode watchdog for generation. The caller
// holds mu.
func (kc *KakaoClient) armWatchdogLocked(generation uint64, delay time.Duration) {
	kc.stopWatchdogLocked()
	var timer stopper
	timer = kc.watchdogAfter(delay, func() { kc.episodeExpired(generation, timer) })
	kc.watchdog = timer
}

// endEpisode ends generation's recovery episode without a completed login.
func (kc *KakaoClient) endEpisode(generation uint64) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.generation == generation {
		kc.schedule.succeeded()
		kc.stopWatchdogLocked()
	}
}

func (kc *KakaoClient) stopWatchdogLocked() {
	if kc.watchdog != nil {
		kc.watchdog.Stop()
		kc.watchdog = nil
	}
}

// episodeExpired ends a recovery episode that reached its watchdog without
// a completed login. The native client logs out locally here; the bridge
// stops instead, closing any pending attempt and any session whose login
// did not complete, and waits for the operator's reconnect command or a
// process restart. Credentials and the profile are left untouched.
func (kc *KakaoClient) episodeExpired(generation uint64, timer stopper) {
	kc.mu.Lock()
	if kc.watchdog != timer || kc.stopping || kc.generation != generation || !kc.schedule.active() {
		kc.mu.Unlock()
		return
	}
	kc.watchdog = nil
	attempts := kc.schedule.attempt
	kc.mu.Unlock()
	kc.log().Warn().Int("failed_attempts", attempts).Msg("Kakao recovery episode reached its watchdog; stopping until reconnect")
	kc.Disconnect()
	kc.mu.Lock()
	kc.schedule = recoverySchedule{gaveUp: true}
	kc.mu.Unlock()
	kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateRecoveryStopped, Message: "Reconnecting to KakaoTalk failed for 10 minutes; the bridge stopped trying. Send the reconnect command or restart the bridge."})
}

// startRetryLocked waits out delay and then reconnects for generation. The
// caller holds mu.
func (kc *KakaoClient) startRetryLocked(generation uint64, delay time.Duration) {
	wait := kc.wait
	kc.retry.start(&kc.mu, func(ctx context.Context, retryID uint64) {
		if err := wait(ctx, delay); err != nil {
			return
		}
		kc.mu.Lock()
		if kc.stopping || kc.generation != generation || kc.client != nil || kc.cleanup != nil || kc.retry.id != retryID {
			kc.mu.Unlock()
			return
		}
		kc.connecting = true
		kc.connectingGeneration = generation
		connectCtx, connectCancel := context.WithCancel(ctx)
		kc.connectCancel = connectCancel
		kc.mu.Unlock()
		kc.sendState(status.BridgeState{StateEvent: status.StateConnecting})
		kc.connectOnce(connectCtx, generation, true)
		connectCancel()
		kc.mu.Lock()
		if kc.generation == generation {
			kc.connecting = false
			kc.connectCancel = nil
		}
		kc.mu.Unlock()
	})
}

// errDeliveryNotConfirmed marks a catch-up stopped because Matrix did not
// confirm an event; its source progress is retained for replay.
var errDeliveryNotConfirmed = errors.New("connector: delivery not confirmed")

// retryableRecoveryError reports whether a failed attempt retries on the
// episode cadence. As in the official client, every login failure does,
// including LOGINLIST statuses such as -328 and -310, a transport failure
// during LOGINLIST, and a failed token renewal, except those after which the
// native client logs out: LOGINLIST -445 and a renewal that KakaoTalk ends
// (client.ErrSignedOut).
func retryableRecoveryError(err error) bool {
	if err == nil || errors.Is(err, client.ErrSignedOut) {
		return false
	}
	var bootstrapErr bootstrapFailure
	if errors.As(err, &bootstrapErr) {
		if bootstrapErr.stage == "catch-up" {
			// Only Matrix-side delivery is retried here; source failures during
			// catch-up stay terminal. Progress was retained, so replay is safe.
			return errors.Is(bootstrapErr.err, errDeliveryNotConfirmed)
		}
		err = bootstrapErr.err
	}
	var statusErr client.StatusError
	if errors.As(err, &statusErr) {
		return !(statusErr.Command == "LOGINLIST" && statusErr.Status == -445)
	}
	if errors.Is(err, client.ErrLogin) || errors.Is(err, client.ErrCredentialRenewal) {
		return true
	}
	if errors.Is(err, client.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

// shutdownBootstrap releases an owner retained before bootstrap calls. A
// concurrent Disconnect owns cleanup once it marks stopping, so Connect must
// leave that shutdown and any retry to Disconnect.
func (kc *KakaoClient) shutdownBootstrap(c kakaoClient, phase string, force bool) {
	kc.mu.Lock()
	owned := kc.cleanup == c && !kc.retained.busy && (force || !kc.stopping)
	if owned {
		kc.retained.begin()
	}
	kc.mu.Unlock()
	if !owned {
		return
	}
	err := shutdownKakaoClient(c)
	if err != nil {
		kc.log().Err(err).Msg("Failed to clean up Kakao client " + phase)
	}
	kc.mu.Lock()
	kc.release(c, err)
	kc.mu.Unlock()
}

func shutdownKakaoClient(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), terminalDisconnectTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}

// run consumes the typed event stream until the session ends. It is the only
// goroutine that queues remote events or commits, which preserves the
// client's per-chat commit order.
func (kc *KakaoClient) run(c kakaoClient, stream <-chan events.Result, done chan struct{}, generation uint64) {
	kickedOut := false
	var kick events.Kickout
	changeServer := false
	deliveryFailed := false
	var terminalErr error
	metadataResults, stopMetadata := kc.startMetadataRefresh(c)
	// heldTimer wakes the pump for the earliest held-chat retry.
	var heldTimer *time.Timer
	defer func() {
		if heldTimer != nil {
			heldTimer.Stop()
		}
	}()
eventLoop:
	for {
		var result events.Result
		var heldRetry <-chan time.Time
		if heldTimer != nil {
			heldTimer.Stop()
			heldTimer = nil
		}
		if due, ok := kc.nextHeldRetry(); ok && !kickedOut && !changeServer {
			heldTimer = time.NewTimer(time.Until(due))
			heldRetry = heldTimer.C
		}
		select {
		case now := <-heldRetry:
			kc.retryDueHolds(c, now)
			continue
		case <-kc.deliveryResume:
			if !kickedOut && !changeServer {
				kc.resumeHeld(c, 0)
			}
			continue
		case enrichment := <-metadataResults:
			if !kickedOut && !changeServer && kc.applyMetadataRefresh(c, enrichment) {
				break eventLoop
			}
			continue
		case next, ok := <-stream:
			if !ok {
				break eventLoop
			}
			result = next
		}
		if kickedOut || changeServer {
			// A terminal notice ends the session's event acceptance window. The
			// stream still has to close so the owner can publish its terminal
			// bridge state, but later packets must not be committed.
			continue
		}
		if result.Err != nil {
			if errors.Is(result.Err, events.ErrUnidentifiableMembership) {
				terminalErr = events.ErrUnidentifiableMembership
				break eventLoop
			}
			if isUnidentifiableMessageError(result.Err) {
				terminalErr = unidentifiableMessageError()
				break eventLoop
			}
			kc.log().Warn().Err(result.Err).Msg("Dropped undecodable Kakao event")
			continue
		}
		if notice, ok := result.Event.(events.Kickout); ok {
			kickedOut = true
			kick = notice
		}
		if _, ok := result.Event.(events.ChangeServer); ok {
			changeServer = true
		}
		if !kc.deliver(c, result.Event) && !kickedOut && !changeServer {
			// A failed event of no chat cannot be held. Release this session
			// and let bounded connector recovery replay from durable progress.
			// Outbound mutations are never replayed by this path.
			deliveryFailed = true
			break eventLoop
		}
	}
	stopMetadata()
	kc.mu.Lock()
	stopping := kc.stopping
	kc.mu.Unlock()
	switch {
	case stopping:
	case terminalErr != nil:
		classification := stateUnidentifiableMsg
		if errors.Is(terminalErr, events.ErrUnidentifiableMembership) {
			classification = stateGroupMembershipInvalid
		}
		kc.log().Error().Str("classification", string(classification)).Msg("Kakao event admission stopped; operator recovery is required")
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: classification})
	case kickedOut:
		record := newKickoutRecord(kick, kc.now())
		kc.recordKickout(record)
		kc.sendState(kickedOutState(record))
	case changeServer:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateChangeServer})
	case deliveryFailed:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDeliveryPaused})
	default:
		kc.sendState(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: stateDisconnected})
	}
	kc.sessionEnded(c, done, generation, kickedOut, changeServer, terminalErr)
}

func (kc *KakaoClient) sessionEnded(c kakaoClient, done chan struct{}, generation uint64, kickedOut, changeServer bool, terminalErr error) {
	close(done)
	kc.mu.Lock()
	if kc.generation != generation || kc.client != c {
		kc.mu.Unlock()
		return
	}
	stopping := kc.stopping
	kc.client = nil
	kc.done = nil
	if !stopping {
		kc.retain(c)
		kc.retained.begin()
	}
	kc.mu.Unlock()
	if stopping {
		return
	}
	shutdownErr := shutdownWithRecoveryTimeout(c)
	kc.mu.Lock()
	kc.release(c, shutdownErr)
	canRecover := shutdownErr == nil && !kc.stopping && kc.generation == generation
	kc.mu.Unlock()
	// The session loss starts a recovery episode, or continues one whose
	// login never completed.
	recoverSession := func() {
		kc.scheduleRecovery(generation, func(*recoverySchedule, time.Time) (time.Duration, bool) {
			return firstRecoveryDelay, true
		})
	}
	if kickedOut || terminalErr != nil {
		recoverSession = nil
	}
	if shutdownErr != nil {
		kc.log().Err(shutdownErr).Msg("Failed to close Kakao client before recovery")
		kc.scheduleCleanupRetry(c, generation, recoverSession)
		return
	}
	if canRecover && recoverSession != nil {
		recoverSession()
	}
}

// scheduleCleanupRetry keeps a timed-out profile owner attached and permits
// one later cleanup attempt. No replacement can be opened while this owner is
// retained; this is deliberately separate from the recovery attempt budget.
func (kc *KakaoClient) scheduleCleanupRetry(c kakaoClient, generation uint64, recoverSession func()) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.cleanup != c || kc.stopping || kc.cleanupRetry.running() || kc.cleanupRetryAttempts >= 1 {
		return
	}
	kc.cleanupRetryAttempts++
	wait := kc.wait
	kc.cleanupRetry.start(&kc.mu, func(ctx context.Context, retryID uint64) {
		if err := wait(ctx, time.Second); err != nil {
			return
		}
		kc.mu.Lock()
		if kc.cleanup != c || kc.stopping || kc.cleanupRetry.id != retryID {
			kc.mu.Unlock()
			return
		}
		kc.retained.begin()
		kc.mu.Unlock()
		err := shutdownWithRecoveryTimeout(c)
		kc.mu.Lock()
		kc.release(c, err)
		canRecover := err == nil && !kc.stopping && kc.generation == generation
		kc.mu.Unlock()
		if canRecover && recoverSession != nil {
			recoverSession()
		}
	})
}

func shutdownWithRecoveryTimeout(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryCleanupTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}

func (kc *KakaoClient) Disconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), terminalDisconnectTimeout)
	defer cancel()
	leave, ok := kc.enterDisconnect(ctx)
	if !ok {
		return
	}
	defer leave()
	kc.flushReadAcks(ctx)
	stop := kc.beginStop()
	joinWorkers := func() { join(ctx, stop.retryDone, stop.cleanupRetryDone) }
	c := stop.owner
	if c == nil {
		joinWorkers()
		return
	}
	// A recovery cleanup already owns Shutdown. Wait for it, then release the
	// lease without opening a replacement behind its back.
	kc.mu.Lock()
	busy := stop.ownedByOther && kc.retained.busy && kc.cleanup == c
	cleanupDone := kc.retained.done
	kc.mu.Unlock()
	if busy && cleanupDone != nil {
		select {
		case <-cleanupDone:
		case <-ctx.Done():
			return
		}
		joinWorkers()
		return
	}
	err := shutdownWithDeadline(ctx, c, stop.pumpDone)
	if err != nil {
		kc.log().Err(err).Msg("Failed to close Kakao client")
	}
	kc.mu.Lock()
	kc.release(c, err)
	kc.mu.Unlock()
	joinWorkers()
}

// enterDisconnect admits one Disconnect at a time. A caller that is not
// admitted before ctx ends gives up, so concurrent callers share one overall
// timeout budget instead of queueing indefinitely.
func (kc *KakaoClient) enterDisconnect(ctx context.Context) (leave func(), ok bool) {
	kc.mu.Lock()
	if kc.disconnectGate == nil {
		kc.disconnectGate = make(chan struct{}, 1)
	}
	gate := kc.disconnectGate
	kc.mu.Unlock()
	select {
	case gate <- struct{}{}:
		if ctx.Err() != nil {
			<-gate
			return nil, false
		}
		return func() { <-gate }, true
	case <-ctx.Done():
		return nil, false
	}
}

// stopPlan is what one Disconnect must shut down and join.
type stopPlan struct {
	// owner is the profile owner to shut down, nil when none remains.
	owner kakaoClient
	// pumpDone closes when the owner's event pump exits; nil when none runs.
	pumpDone chan struct{}
	// ownedByOther reports that a recovery cleanup was already shutting the
	// owner down when Disconnect began.
	ownedByOther bool
	// retryDone and cleanupRetryDone join the stopped background workers.
	retryDone, cleanupRetryDone <-chan struct{}
}

// beginStop ends the current lifecycle: it cancels the lifecycle context,
// both workers and any connect attempt, supersedes the generation, and moves
// the active client, if any, to the retained owner with a shutdown in flight.
func (kc *KakaoClient) beginStop() stopPlan {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.reactionView != nil && kc.reactionView.cancel != nil {
		kc.reactionView.cancel()
	}
	kc.reactionView = nil
	if kc.lifecycleCancel != nil {
		kc.lifecycleCancel()
		kc.lifecycle, kc.lifecycleCancel = nil, nil
	}
	plan := stopPlan{retryDone: kc.retry.stop(), cleanupRetryDone: kc.cleanupRetry.stop()}
	if kc.connectCancel != nil {
		kc.connectCancel()
	}
	kc.generation++
	c := kc.client
	done := kc.done
	if c != nil && done != nil {
		kc.retained.pumpDone = done
	}
	if c == nil {
		c = kc.cleanup
		// A retained owner keeps the original event-pump completion signal.
		// Retry cleanup must join that same worker before releasing ownership.
		done = kc.retained.pumpDone
	}
	plan.owner, plan.pumpDone = c, done
	plan.ownedByOther = c != nil && kc.retained.busy
	kc.stopping = true
	kc.client = nil
	kc.done = nil
	if c != nil && !kc.retained.busy {
		kc.retained.begin()
	}
	if c != nil {
		kc.cleanup = c
	}
	return plan
}

// shutdownWithDeadline shuts c down and then waits, within ctx's deadline,
// for its event pump to exit. A pump that outlives the deadline is reported as
// DeadlineExceeded so the owner stays retained until a later cleanup joins it.
func shutdownWithDeadline(ctx context.Context, c kakaoClient, pumpDone chan struct{}) error {
	err := c.Shutdown(ctx)
	if err != nil || pumpDone == nil {
		return err
	}
	deadline, hasDeadline := ctx.Deadline()
	wait := time.Until(deadline)
	if !hasDeadline || wait <= 0 {
		return context.DeadlineExceeded
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-pumpDone:
		return nil
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

// worker is a cancellable background goroutine with a completion signal.
// Each start bumps id, so a run that has been superseded or stopped can tell
// it no longer owns the slot. The lifecycle mutex guards every field.
type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
	id     uint64
}

// start runs fn in a new goroutine, replacing the slot's earlier run. The
// caller holds mu; the goroutine takes it again to clear the slot when fn
// returns, unless a later start or stop has already superseded it.
func (w *worker) start(mu *sync.Mutex, fn func(ctx context.Context, id uint64)) {
	ctx, cancel := context.WithCancel(context.Background())
	w.id++
	id := w.id
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
	go func() {
		defer func() {
			close(done)
			mu.Lock()
			if w.id == id {
				w.cancel, w.done = nil, nil
			}
			mu.Unlock()
		}()
		fn(ctx, id)
	}()
}

// stop cancels the current run and supersedes it. The caller holds mu and
// joins the returned channel, nil when nothing was running, after releasing it.
func (w *worker) stop() <-chan struct{} {
	if w.cancel != nil {
		w.cancel()
	}
	done := w.done
	w.cancel, w.done = nil, nil
	w.id++
	return done
}

// running reports whether a run is in progress. The caller holds mu.
func (w *worker) running() bool { return w.cancel != nil }

// join waits for each non-nil channel to close, or for ctx to end.
func join(ctx context.Context, done ...<-chan struct{}) bool {
	for _, ch := range done {
		if ch == nil {
			continue
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// ownerCleanup tracks the shutdown of the retained profile owner,
// KakaoClient.cleanup. The lifecycle mutex guards every field.
type ownerCleanup struct {
	// pumpDone remains associated with the owner while a Disconnect timeout
	// leaves its event pump running. It must not be discarded when the active
	// client fields are cleared: a later cleanup attempt must join the same
	// generation before releasing the profile owner.
	pumpDone chan struct{}
	// done closes when the in-flight Shutdown of the owner returns.
	done chan struct{}
	// busy marks a Shutdown of the owner in flight.
	busy bool
}

// begin marks a Shutdown of the owner in flight.
func (o *ownerCleanup) begin() {
	o.busy = true
	if o.done == nil {
		o.done = make(chan struct{})
	}
}

// retain makes c the profile owner that must be shut down before another
// session can open, with no shutdown in flight. The caller holds mu.
func (kc *KakaoClient) retain(c kakaoClient) {
	kc.cleanup = c
	kc.retained = ownerCleanup{}
}

// release ends a Shutdown attempt of c. If c is still the retained owner the
// attempt is no longer in flight, and a successful one releases the owner.
// A failed attempt keeps c retained so a later Disconnect or retry can finish
// it. The caller holds mu.
func (kc *KakaoClient) release(c kakaoClient, err error) {
	if kc.cleanup != c {
		return
	}
	kc.retained.busy = false
	if kc.retained.done != nil {
		close(kc.retained.done)
		kc.retained.done = nil
	}
	if err == nil {
		kc.cleanup = nil
		kc.retained.pumpDone = nil
	}
}

// metadataClient returns the client that may serve metadata requests: the
// connected session, or the bootstrap owner while it connects.
func (kc *KakaoClient) metadataClient() (kakaoClient, error) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.stopping {
		return nil, bridgev2.ErrNotLoggedIn
	}
	if kc.client != nil {
		return kc.client, nil
	}
	if kc.connecting && kc.cleanup != nil {
		return kc.cleanup, nil
	}
	return nil, bridgev2.ErrNotLoggedIn
}

var (
	errDetachedKickedOut       = errors.New("connector: KakaoTalk signed this device out; log out of the bridge and log in again instead")
	errDetachedRetryPending    = errors.New("connector: the bridge is reconnecting to KakaoTalk; wait for the bridge state to report the outcome, then run this command again")
	errDetachedRecoveryStopped = errors.New("connector: reconnecting to KakaoTalk failed and the bridge stopped trying; send the reconnect command first")
)

// openDetached opens a temporary profile client outside the connection
// lifecycle. It refuses while a session is connecting, connected, stopping or
// still retaining an owner, since the profile lease admits one owner.
//
// A detached client logs in, so it passes the same gates as the supervisor's
// recovery: no login after KICKOUT, none while a recovery episode is running,
// and none after the episode watchdog stopped recovery until the operator
// reconnects. The reconciliation commands still need it when no gate applies,
// because an unresolved group creation fails every bootstrap until it is
// reconciled; such a failure ends the episode.
func (kc *KakaoClient) openDetached() (kakaoClient, error) {
	if _, kicked := kc.kickedOut(); kicked {
		return nil, errDetachedKickedOut
	}
	kc.mu.Lock()
	busy := kc.stopping || kc.connecting || kc.cleanup != nil
	pending := kc.retry.running() || kc.schedule.active()
	gaveUp := kc.schedule.gaveUp
	kc.mu.Unlock()
	if gaveUp {
		return nil, errDetachedRecoveryStopped
	}
	if pending {
		return nil, errDetachedRetryPending
	}
	if busy || kc.open == nil {
		return nil, errors.New("connector: profile cleanup or connection is still in progress")
	}
	c, err := kc.open()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// releaseDetached shuts down a client from openDetached. When shutdown fails
// and no other owner is retained, c becomes the retained owner so a later
// Disconnect finishes its cleanup.
func (kc *KakaoClient) releaseDetached(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryCleanupTimeout)
	defer cancel()
	err := c.Shutdown(ctx)
	if err != nil {
		kc.mu.Lock()
		if kc.cleanup == nil {
			kc.retain(c)
		}
		kc.mu.Unlock()
	}
	return err
}
