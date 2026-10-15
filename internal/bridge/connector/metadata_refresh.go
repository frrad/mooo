package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

type metadataRefreshKind uint8

const (
	refreshMembers metadataRefreshKind = iota
	refreshPersonal
	refreshBoards
	refreshRoomInfo
	// retryMembership retries the authoritative membership refresh of a group
	// that a failed refresh left paused. It is scheduled by that failure, with
	// backoff, never polled: the official client reads MEMLIST only from its
	// member-list views and learns membership from login deltas and notices.
	retryMembership
)
const maxMetadataRefreshJobs = 1024

// Failed tasks back off exponentially from the profile attempt cooldown, so a
// persistent failure costs at most one request per maxMetadataRetryDelay.
const maxMetadataRetryDelay = time.Hour

// maxMetadataRetries bounds the retries of a failed optional read: member
// profiles, personal metadata, Boards and room info. The official client
// does not retry them at all; it clears its in-flight state on failure and
// refetches on the next native trigger (research/bridge/NATIVE-SKEW-AUDIT.md
// finding 15). The bridge keeps a few spaced retries (2, 4 and 8 minutes) so
// a brief outage does not leave a ghost or room unenriched until that
// trigger, then drops the task; the trigger enqueues it again.
//
// A paused group's retryMembership is not bounded: the group forwards
// nothing until its roster converges, so the bridge needs it to function. It
// still backs off to one attempt per maxMetadataRetryDelay.
const maxMetadataRetries = 3

type metadataRefreshTask struct {
	kind     metadataRefreshKind
	chatID   int64
	version  uint64
	snapshot chatSnapshot
	ids      []int64
	// refetch lists ids whose usable stored profile is still refetched: the
	// official client sends MEMBER for every NEWMEM invitee.
	refetch   []int64
	notBefore time.Time
	attempts  int
	// personalRevision is the login-list mcmRevision a personal read answers.
	personalRevision int32
}

// versioned reports whether the task belongs to one remembered room snapshot.
// Account personal metadata and roster checks do not.
func (t metadataRefreshTask) versioned() bool {
	return t.kind != refreshPersonal && t.kind != retryMembership
}

func metadataRetryDelay(attempts int) time.Duration {
	delay := memberProfileAttemptCooldown
	for i := 1; i < attempts && delay < maxMetadataRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, maxMetadataRetryDelay)
}

func (t metadataRefreshTask) key() string {
	if t.kind == refreshPersonal {
		return "personal"
	}
	return fmt.Sprintf("%d/%d", t.kind, t.chatID)
}

type metadataRefreshQueue struct {
	mu        sync.Mutex
	pending   map[string]metadataRefreshTask
	order     []string
	snapshots map[int64]metadataRefreshTask
	version   uint64
	wake      chan struct{}
	// dropped is told when the bounded queue rejects a task.
	dropped func()
}

func newMetadataRefreshQueue() *metadataRefreshQueue {
	return &metadataRefreshQueue{pending: map[string]metadataRefreshTask{}, snapshots: map[int64]metadataRefreshTask{}, wake: make(chan struct{}, 1)}
}
func (q *metadataRefreshQueue) remember(snap chatSnapshot) metadataRefreshTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	if previous, ok := q.snapshots[snap.data.ChatID]; ok && previous.snapshot.personalRequired && snap.data.Meta == nil {
		snap.personalRequired = true
	}
	q.version++
	task := metadataRefreshTask{chatID: snap.data.ChatID, version: q.version, snapshot: snap, ids: slices.Clone(snap.profileIDs())}
	q.snapshots[task.chatID] = task
	return task
}
func (q *metadataRefreshQueue) offer(task metadataRefreshTask) {
	q.mu.Lock()
	if task.versioned() {
		latest, ok := q.snapshots[task.chatID]
		if !ok || latest.version != task.version {
			q.mu.Unlock()
			return
		}
	}
	key := task.key()
	if previous, found := q.pending[key]; found {
		if task.kind == refreshMembers && previous.version == task.version {
			merged := slices.Clone(previous.ids)
			seen := map[int64]bool{}
			for _, userID := range merged {
				seen[userID] = true
			}
			for _, userID := range task.ids {
				if !seen[userID] {
					merged = append(merged, userID)
					seen[userID] = true
				}
			}
			task.ids = merged
			for _, userID := range previous.refetch {
				if !slices.Contains(task.refetch, userID) {
					task.refetch = append(task.refetch, userID)
				}
			}
		}
		task.personalRevision = max(task.personalRevision, previous.personalRevision)
		task.notBefore = maxRefreshTime(task.notBefore, previous.notBefore)
		task.attempts = max(task.attempts, previous.attempts)
	} else if len(q.pending) >= maxMetadataRefreshJobs {
		q.mu.Unlock()
		// The next session's discovery reconstructs demand.
		if q.dropped != nil {
			q.dropped()
		}
		return
	} else {
		q.order = append(q.order, key)
	}
	q.pending[key] = task
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func maxRefreshTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (q *metadataRefreshQueue) take(now time.Time) (metadataRefreshTask, bool, time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	wait := time.Hour
	for i, key := range q.order {
		task := q.pending[key]
		if task.notBefore.After(now) {
			wait = min(wait, task.notBefore.Sub(now))
			continue
		}
		q.order = slices.Delete(q.order, i, i+1)
		delete(q.pending, key)
		return task, true, 0
	}
	return metadataRefreshTask{}, false, wait
}
func (q *metadataRefreshQueue) current(task metadataRefreshTask) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	latest, ok := q.snapshots[task.chatID]
	return ok && latest.version == task.version
}
func (q *metadataRefreshQueue) rooms() []metadataRefreshTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]metadataRefreshTask, 0, len(q.snapshots))
	for _, task := range q.snapshots {
		out = append(out, task)
	}
	slices.SortFunc(out, func(a, b metadataRefreshTask) int {
		if a.chatID < b.chatID {
			return -1
		}
		if a.chatID > b.chatID {
			return 1
		}
		return 0
	})
	return out
}

func (kc *KakaoClient) enqueueEnrichment(snap chatSnapshot, cache roomMetadataCache) {
	task := kc.metadataRefresh.remember(snap)
	task.kind = refreshMembers
	kc.metadataRefresh.offer(task)
	if snap.data.Type == "DirectChat" && snap.data.Meta != nil && snap.data.Meta.ImageURL != "" || snap.data.Type == "MultiChat" && len(snap.data.ChatMetas) == 0 && snap.data.Meta == nil {
		task.kind = refreshRoomInfo
		kc.metadataRefresh.offer(task)
	}
	if snap.data.Type != "MultiChat" {
		return
	}
	// Personal metadata is read once per room; afterwards the login-list
	// revision and CHGMCMETA payloads drive it.
	if snap.data.Meta == nil && cache.PersonalAt.IsZero() {
		task.kind = refreshPersonal
		kc.metadataRefresh.offer(task)
	}
	// Boards are read once; afterwards pushes and chat-entry revisions drive them.
	if cache.BoardsAt.IsZero() {
		task.kind = refreshBoards
		kc.metadataRefresh.offer(task)
	}
}

type metadataRefreshResult struct {
	task     metadataRefreshTask
	profiles map[int64]*bridgev2.UserInfo
	info     *bridgev2.ChatInfo
	trace    *profileRefreshTrace
	err      error
	// sourceErr is the subset of err returned by a Kakao read. Only it can
	// signal a failed session; Matrix and database errors never can.
	sourceErr error
	// reconcile asks the event pump for an authoritative membership refresh.
	reconcile bool
}

// The worker starts after bootstrap. Queue residence is independent of callers'
// deadlines; each physical read keeps the SDK's account-wide pacing. Results
// are published only by the event pump, and never carry a membership map.
func (kc *KakaoClient) startMetadataRefresh(c kakaoClient) (<-chan metadataRefreshResult, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan metadataRefreshResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			task, ok, wait := kc.metadataRefresh.take(time.Now())
			if !ok {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-kc.metadataRefresh.wake:
					timer.Stop()
				case <-timer.C:
				}
				continue
			}
			taskCtx, finish := context.WithTimeout(ctx, 2*time.Minute)
			result := kc.performMetadataRefresh(taskCtx, c, task)
			finish()
			select {
			case <-ctx.Done():
				return
			case results <- result:
			}
		}
	}()
	return results, func() { cancel(); <-done }
}

func (kc *KakaoClient) metadataRoomAllowed(ctx context.Context, task metadataRefreshTask) bool {
	if !kc.metadataRefresh.current(task) {
		return false
	}
	p, err := kc.store.portalByKey(ctx, makePortalKey(task.chatID, kc.login.ID))
	if err != nil || p == nil || p.MXID == "" {
		return false
	}
	if meta, ok := p.Metadata.(*KakaoPortalMetadata); ok && meta != nil && (meta.SourceRemoved || meta.MembershipPending) {
		return false
	}
	return true
}

func (kc *KakaoClient) performMetadataRefresh(ctx context.Context, c kakaoClient, task metadataRefreshTask) metadataRefreshResult {
	trace := newProfileRefreshTrace(c)
	result := metadataRefreshResult{task: task, trace: trace}
	c = trace
	if task.versioned() && !kc.metadataRoomAllowed(ctx, task) {
		return result
	}
	switch task.kind {
	case refreshMembers:
		batch := task.ids[:min(len(task.ids), chatmeta.MaxMemberBatch)]
		profiles, err := kc.resolveMemberProfiles(ctx, c, task.chatID, batch, task.refetch)
		result.err, result.sourceErr = err, err
		result.profiles = map[int64]*bridgev2.UserInfo{}
		for _, profile := range kc.cacheProfiles(profiles, batch) {
			info := userInfoForMember(profile, kc)
			ghost, loadErr := kc.login.Bridge.DB.Ghost.GetByID(ctx, makeUserID(profile.UserID))
			if loadErr != nil {
				result.err = loadErr
				continue
			}
			if ghost == nil {
				continue
			} // Room projection creates identities first.
			if info.Avatar != nil && !info.Avatar.Remove {
				if ghost.AvatarID == info.Avatar.ID && ghost.AvatarSet && ghost.AvatarMXC != "" {
					info.Avatar.MXC, info.Avatar.Hash = ghost.AvatarMXC, ghost.AvatarHash
				} else {
					uri, hash, uploadErr := info.Avatar.Reupload(ctx, kc.login.Bridge.Matrix.GhostIntent(makeUserID(profile.UserID)), ghost.AvatarHash, ghost.AvatarMXC)
					if uploadErr != nil {
						result.err = uploadErr
						info.Avatar = nil
					} else {
						info.Avatar.MXC, info.Avatar.Hash = uri, hash
					}
				}
				if info.Avatar != nil {
					info.Avatar.Get = nil
				}
			}
			result.profiles[profile.UserID] = info
		}
		if len(task.ids) > len(batch) && kc.metadataRefresh.current(task) {
			task.ids = slices.Clone(task.ids[len(batch):])
			kc.metadataRefresh.offer(task)
		}
	case refreshPersonal:
		selectedRooms := kc.metadataRefresh.rooms()
		rooms, err := c.PersonalMetas(ctx)
		result.err, result.sourceErr = err, err
		if err != nil {
			break
		}
		if task.personalRevision != 0 {
			if err = kc.recordPersonalRevision(ctx, task.personalRevision); err != nil {
				result.err = err
			}
		}
		for _, selected := range selectedRooms {
			if selected.snapshot.data.Type != "MultiChat" || !kc.metadataRoomAllowed(ctx, selected) {
				continue
			}
			cache, err := kc.readRoomMetadata(ctx, selected.chatID)
			if err != nil {
				result.err = err
				continue
			}
			// An omitted room is unavailable, not confirmation of a personal clear.
			if meta, ok := rooms[selected.chatID]; ok {
				cache.Personal = &meta
			}
			cache.PersonalAt = kc.now()
			if err = kc.publishRoomMetadata(ctx, selected, cache); err != nil {
				result.err = err
				continue
			}
			selected.kind = refreshBoards // Reproject cached optional metadata below.
			selected.notBefore = time.Time{}
			kc.metadataRefresh.offer(selected)
		}
	case refreshRoomInfo:
		response := chatmeta.ChatInfoResponse{ChatData: task.snapshot.data}
		var err error
		if response.ChatData.Type == "MultiChat" {
			response, err = c.ChatInfo(ctx, task.chatID)
		}
		if err != nil {
			result.err, result.sourceErr = err, err
			break
		}
		if response.ChatData.ChatID != task.chatID || response.ChatData.LinkID != 0 || response.ChatData.Type != task.snapshot.data.Type {
			result.err = errChatInfoMismatch
			break
		}
		if !kc.metadataRefresh.current(task) {
			break
		}
		p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(task.chatID, kc.login.ID))
		if err != nil {
			result.err = err
			break
		}
		data := response.ChatData
		personalKnown := true
		if data.Type == "MultiChat" && data.Meta == nil {
			// CHATINFO omits personal settings; a personal name or image must
			// not be replaced by the shared one.
			cache, cacheErr := kc.readRoomMetadata(ctx, task.chatID)
			if cacheErr != nil {
				result.err = cacheErr
				break
			}
			data.Meta = cache.Personal
			personalKnown = cache.Personal != nil || !task.snapshot.personalRequired
		}
		if data.Type == "MultiChat" {
			data, err = kc.checkpointGroupDisplay(ctx, p, data)
		}
		if err != nil {
			result.err = err
			break
		}
		snap := task.snapshot
		snap.data = data
		snap.profiles = nil
		snap.deferred = false
		info, err := kc.projectChatInfo(p, snap)
		if err != nil {
			result.err = err
			break
		}
		info.Members, info.UserLocal, info.Topic, info.ExtraUpdates = nil, nil, nil, nil
		if !personalKnown {
			info.Name, info.Avatar = nil, nil
		}
		if info.Avatar != nil && !info.Avatar.Remove {
			uri, hash, uploadErr := info.Avatar.Reupload(ctx, kc.login.Bridge.Bot, p.AvatarHash, p.AvatarMXC)
			if uploadErr != nil {
				result.err = uploadErr
				info.Avatar = nil
			} else {
				info.Avatar.MXC, info.Avatar.Hash, info.Avatar.Get = uri, hash, nil
			}
		}
		result.info = info
	case refreshBoards:
		cache, err := kc.readRoomMetadata(ctx, task.chatID)
		if err != nil {
			result.err = err
			break
		}
		if cache.BoardsAt.IsZero() {
			moim, err := c.MoimMeta(ctx, task.chatID)
			if err != nil {
				result.err, result.sourceErr = err, err
				break
			}
			if moim.ChatID != task.chatID {
				result.err = errChatInfoMismatch
				break
			}
			cache.Boards, cache.BoardsAt = moim.Metas, kc.now()
			if err = kc.publishRoomMetadata(ctx, task, cache); err != nil {
				result.err = err
				break
			}
		}
		p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(task.chatID, kc.login.ID))
		if err != nil {
			result.err = err
			break
		}
		snap := task.snapshot
		if cache.Personal != nil {
			snap.data.Meta = cache.Personal
		}
		snap.moimMetas, snap.moimKnown = cache.Boards, !cache.BoardsAt.IsZero()
		snap.profiles = nil
		snap.deferred = false
		snap.data, err = kc.checkpointGroupDisplay(ctx, p, snap.data)
		if err != nil {
			result.err = err
			break
		}
		info, err := kc.projectChatInfo(p, snap)
		if err != nil {
			result.err = err
			break
		}
		info.Members, info.UserLocal = nil, nil
		if cache.Personal == nil && task.snapshot.data.Meta == nil && task.snapshot.personalRequired {
			info.Name, info.Avatar = nil, nil
		}
		if info.Avatar != nil && !info.Avatar.Remove {
			uri, hash, uploadErr := info.Avatar.Reupload(ctx, kc.login.Bridge.Bot, p.AvatarHash, p.AvatarMXC)
			if uploadErr != nil {
				result.err = uploadErr
				info.Avatar = nil
			} else {
				info.Avatar.MXC, info.Avatar.Hash, info.Avatar.Get = uri, hash, nil
			}
		}
		result.info = info
	case retryMembership:
		p, err := kc.store.portalByKey(ctx, makePortalKey(task.chatID, kc.login.ID))
		if err != nil {
			result.err = err
			break
		}
		if p == nil || p.MXID == "" {
			break
		}
		meta, ok := p.Metadata.(*KakaoPortalMetadata)
		if !ok || meta == nil || meta.SourceRemoved {
			break
		}
		// A notice or another refresh may have already converged the group.
		result.reconcile = meta.MembershipPending
	}
	return result
}

// scheduleMembershipRetry queues a paused group's refresh after backoff.
func (kc *KakaoClient) scheduleMembershipRetry(chatID int64, attempts int) {
	kc.metadataRefresh.offer(metadataRefreshTask{kind: retryMembership, chatID: chatID, attempts: attempts, notBefore: time.Now().Add(metadataRetryDelay(attempts))})
}

// No source I/O happens here. Pre-uploaded avatar results avoid admission waits
// in the pump. Optional results cannot join, leave, create or pause a room.
func (kc *KakaoClient) applyMetadataRefresh(c kakaoClient, result metadataRefreshResult) bool {
	kc.mu.Lock()
	active := !kc.stopping && kc.client == c
	kc.mu.Unlock()
	if !active {
		return false
	}
	if result.err != nil {
		kc.log().Warn().Str("error_class", sourceErrorClass(result.err)).Uint8("refresh_kind", uint8(result.task.kind)).Array("steps", result.trace).Msg("Background metadata refresh deferred")
		if connectionFailure(result.sourceErr) {
			return true
		}
		retry := result.task
		retry.attempts++
		if retry.kind == retryMembership || retry.attempts <= maxMetadataRetries {
			retry.notBefore = time.Now().Add(metadataRetryDelay(retry.attempts))
			kc.metadataRefresh.offer(retry)
		} else {
			kc.log().Info().Uint8("refresh_kind", uint8(retry.kind)).Int("attempts", retry.attempts).Msg("Background metadata refresh abandoned until its next trigger")
		}
	}
	if result.reconcile {
		// The same authoritative path a membership notice takes. It keeps
		// the group paused until the fresh roster has converged in Matrix.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, ok := kc.managedMembershipEvent(ctx, c, result.task.chatID, false); !ok {
			kc.scheduleMembershipRetry(result.task.chatID, result.task.attempts+1)
		} else {
			// Delivery held behind the paused membership can resume.
			kc.resumeHeld(c, result.task.chatID)
		}
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !kc.metadataRoomAllowed(ctx, result.task) {
		return false
	}
	for userID, info := range result.profiles {
		ghostCtx, ghostCancel := context.WithTimeout(context.Background(), 10*time.Second)
		ghost, err := kc.login.Bridge.GetGhostByID(ghostCtx, makeUserID(userID))
		if err != nil {
			kc.log().Warn().Str("error_class", sourceErrorClass(err)).Msg("Could not load ghost for background profile")
		} else {
			ghost.UpdateInfo(ghostCtx, info)
		}
		ghostCancel()
	}
	if result.info != nil {
		kc.queue(&simplevent.ChatInfoChange{EventMeta: simplevent.EventMeta{Type: bridgev2.RemoteEventChatInfoChange, PortalKey: makePortalKey(result.task.chatID, kc.login.ID), Sender: kc.selfSender()}, ChatInfoChange: &bridgev2.ChatInfoChange{ChatInfo: result.info}})
	}
	return false
}

// A local caller deadline is not evidence that the source connection died.
func connectionFailure(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, client.ErrClosed) || errors.Is(err, client.ErrClientClosed) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func (kc *KakaoClient) snapshotUserInfo(profile chatmeta.Member, deferred bool) *bridgev2.UserInfo {
	info := userInfoForMember(profile, kc)
	if deferred && info.Avatar != nil && !info.Avatar.Remove {
		info.Avatar = nil
	}
	return info
}
func (kc *KakaoClient) enqueueMemberEnrichment(chatID int64, ids []int64) {
	q := kc.metadataRefresh
	q.mu.Lock()
	task, ok := q.snapshots[chatID]
	q.mu.Unlock()
	if !ok {
		task = q.remember(chatSnapshot{data: chatmeta.ChatData{ChatID: chatID, Type: "MultiChat"}, roster: slices.Clone(ids), deferred: true})
	}
	if len(ids) == 0 {
		ids = task.snapshot.profileIDs()
	}
	task.kind, task.ids = refreshMembers, slices.Clone(ids)
	q.offer(task)
}

// rememberFeedRoster records a roster derived from a membership feed in the
// room's remembered snapshot, without invalidating its pending enrichment,
// and queues MEMBER for the feed's invitees.
func (kc *KakaoClient) rememberFeedRoster(chatID int64, roster, invitees []int64) {
	q := kc.metadataRefresh
	q.mu.Lock()
	task, ok := q.snapshots[chatID]
	if ok {
		task.snapshot.roster = slices.Clone(roster)
		q.snapshots[chatID] = task
	}
	q.mu.Unlock()
	if !ok {
		task = q.remember(chatSnapshot{data: chatmeta.ChatData{ChatID: chatID, Type: "MultiChat"}, roster: slices.Clone(roster), deferred: true})
	}
	if len(invitees) == 0 {
		return
	}
	task.kind, task.ids, task.refetch = refreshMembers, slices.Clone(invitees), slices.Clone(invitees)
	q.offer(task)
}

func (kc *KakaoClient) invalidateEnrichment(ctx context.Context, chatID int64, kind metadataRefreshKind) {
	q := kc.metadataRefresh
	q.mu.Lock()
	task, ok := q.snapshots[chatID]
	if ok {
		q.version++
		task.version = q.version
		if kind == refreshPersonal {
			task.snapshot.personalRequired = true
		}
		q.snapshots[chatID] = task
	}
	// Expire the checkpoint even without a snapshot, so the next room read
	// enqueues a fresh source read instead of reusing stale enrichment.
	cache, err := kc.readRoomMetadata(ctx, chatID)
	if err == nil {
		if kind == refreshPersonal {
			cache.PersonalAt = time.Time{}
		} else {
			cache.BoardsAt = time.Time{}
		}
		err = kc.writeRoomMetadata(ctx, cache)
	}
	q.mu.Unlock()
	if err != nil {
		kc.log().Warn().Str("error_class", sourceErrorClass(err)).Msg("Could not invalidate room enrichment")
	}
	if !ok {
		return
	}
	task.kind = kind
	q.offer(task)
}

func (kc *KakaoClient) enqueueExistingRoomEnrichment(ctx context.Context, chatID int64, kind metadataRefreshKind, force bool) bool {
	q := kc.metadataRefresh
	q.mu.Lock()
	task, ok := q.snapshots[chatID]
	q.mu.Unlock()
	if !ok || task.snapshot.data.Type != "MultiChat" {
		return false
	}
	if force {
		kc.invalidateEnrichment(ctx, chatID, kind)
		return true
	}
	cache, err := kc.readRoomMetadata(ctx, chatID)
	if err != nil {
		return false
	}
	// A stored checkpoint is reprojected; only a room never read is fetched.
	// Neither kind is reread by age.
	if kind == refreshBoards || cache.PersonalAt.IsZero() {
		task.kind = kind
		q.offer(task)
	}
	return true
}

// Serialize generation validation and optional cache publication with notice
// invalidation. A response begun before a newer notice cannot mark it fresh.
func (kc *KakaoClient) publishRoomMetadata(ctx context.Context, task metadataRefreshTask, record roomMetadataCache) error {
	q := kc.metadataRefresh
	q.mu.Lock()
	defer q.mu.Unlock()
	latest, ok := q.snapshots[task.chatID]
	if !ok || latest.version != task.version {
		return nil
	}
	return kc.writeRoomMetadata(ctx, record)
}

// noteMessageAuthor follows the official client's message conversion, which
// sends MEMBER for an author without a record or with an empty nickname. The
// queue worker skips authors whose stored profile is usable, so a known
// author costs no request. Rooms not yet projected are left to their snapshot.
func (kc *KakaoClient) noteMessageAuthor(chatID, authorID int64) {
	if authorID <= 0 || authorID == kc.userID || kc.metadataRefresh == nil {
		return
	}
	q := kc.metadataRefresh
	q.mu.Lock()
	task, ok := q.snapshots[chatID]
	q.mu.Unlock()
	if !ok {
		return
	}
	task.kind, task.ids = refreshMembers, []int64{authorID}
	q.offer(task)
}
