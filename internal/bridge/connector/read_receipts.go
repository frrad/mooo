package connector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

var errReadReceiptMutation = errors.New("connector: Kakao read acknowledgement failed")

// readReceiptCoalesceWindow is how long a Matrix read receipt waits before it
// is acknowledged to Kakao. Receipts for one chat within the window collapse
// into one acknowledgement of the latest position. An active Matrix client
// sends a receipt per incoming message; the official client never acknowledges
// at that rate. This is bridge policy, not a traced native interval.
const readReceiptCoalesceWindow = 3 * time.Second

// readAckTimeout bounds one deferred acknowledgement request.
const readAckTimeout = 30 * time.Second

// stopper is the part of *time.Timer the coalescer uses.
type stopper interface{ Stop() bool }

// pendingReadAck is one chat's acknowledgement waiting out its window.
type pendingReadAck struct {
	logID int64
	timer stopper
}

// readReceiptTargetWindow bounds how many recent bridged messages are scanned
// when a DECUNREAD watermark names a log the bridge never stored.
const readReceiptTargetWindow = 100

var _ bridgev2.ReadReceiptHandlingNetworkAPI = (*KakaoClient)(nil)

// readReceiptEvent turns a DECUNREAD notice into a Matrix read receipt from
// the member whose watermark advanced. A notice for this login's own user
// marks the room read for the Matrix user through double puppeting. Notices
// for chats or positions the bridge never stored return nil.
func (kc *KakaoClient) readReceiptEvent(ctx context.Context, notice events.ReadStateChanged) (bridgev2.RemoteEvent, error) {
	target, err := kc.readReceiptTarget(ctx, notice.ChatID, notice.Watermark)
	if err != nil || target == "" {
		return nil, err
	}
	return &simplevent.Receipt{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReadReceipt,
			PortalKey: makePortalKey(notice.ChatID, kc.login.ID),
			Sender:    kc.senderFor(notice.UserID),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Int64("kakao_chat_id", notice.ChatID).Int64("kakao_watermark", notice.Watermark)
			},
		},
		LastTarget: target,
	}, nil
}

// handleReadState bridges one DECUNREAD notice. Receipts carry no message
// position, so a failure is logged and dropped rather than replayed; it never
// stops the event loop.
func (kc *KakaoClient) handleReadState(notice events.ReadStateChanged) bool {
	// Live notices and viewing recovery run on different goroutines; the
	// forward-only check and its write must not interleave.
	kc.readStateMu.Lock()
	defer kc.readStateMu.Unlock()
	ctx := context.Background()
	stored := kc.memberReadWatermark(ctx, notice.ChatID, notice.UserID)
	if notice.Watermark <= stored {
		// A member's receipt only moves forward, including after a restart.
		return true
	}
	remote, err := kc.readReceiptEvent(ctx, notice)
	if err != nil {
		kc.log().Warn().Msg("Kakao read receipt target lookup failed")
		return false
	}
	if remote == nil {
		kc.log().Debug().Msg("Ignoring Kakao read receipt for a chat or position that was never bridged")
		return true
	}
	if result := kc.queue(remote); !committable(result) {
		kc.log().Warn().Err(result.Error).Msg("Kakao read receipt was not bridged")
		return false
	}
	kc.setMemberReadWatermark(ctx, notice.ChatID, notice.UserID, notice.Watermark)
	return true
}

func memberReadWatermarkKey(login string, chatID, userID int64) string {
	return fmt.Sprintf("kakao:read-watermark:%s:%d:%d", login, chatID, userID)
}

// memberReadWatermark returns the highest watermark bridged for one member,
// or zero when none was recorded.
func (kc *KakaoClient) memberReadWatermark(ctx context.Context, chatID, userID int64) int64 {
	raw, _, _ := kc.store.get(ctx, memberReadWatermarkKey(string(kc.login.ID), chatID, userID))
	value, _ := strconv.ParseInt(raw, 10, 64)
	return value
}

func (kc *KakaoClient) setMemberReadWatermark(ctx context.Context, chatID, userID, watermark int64) {
	if err := kc.store.put(ctx, memberReadWatermarkKey(string(kc.login.ID), chatID, userID), strconv.FormatInt(watermark, 10)); err != nil {
		kc.log().Warn().Err(err).Msg("Could not store a member read watermark")
	}
}

// readReceiptTarget returns the bridged message at the watermark or, failing
// that, the latest bridged message of the chat before it. Kakao log IDs
// increase within a chat, so the comparison is by log ID, not timestamp.
func (kc *KakaoClient) readReceiptTarget(ctx context.Context, chatID, watermark int64) (networkid.MessageID, error) {
	exact := makeMessageID(chatID, watermark)
	row, err := kc.store.lastMessagePart(ctx, kc.login.ID, exact)
	if err != nil {
		return "", err
	}
	if row != nil && !row.HasFakeMXID() {
		return exact, nil
	}
	recent, err := kc.store.lastNMessagesInPortal(ctx, makePortalKey(chatID, kc.login.ID), readReceiptTargetWindow)
	if err != nil {
		return "", err
	}
	var best int64
	var target networkid.MessageID
	for _, row := range recent {
		rowChat, logID, err := parseMessageID(row.ID)
		if err != nil || rowChat != chatID || row.HasFakeMXID() || logID > watermark || logID <= best {
			continue
		}
		best, target = logID, row.ID
	}
	return target, nil
}

// HandleMatrixReadReceipt acknowledges the receipted Kakao message after the
// chat's coalescing window. The client persists each successful
// acknowledgement, so a repeated receipt is not re-sent; a failed one is
// logged without retrying, matching the shared request path.
func (kc *KakaoClient) HandleMatrixReadReceipt(ctx context.Context, receipt *bridgev2.MatrixReadReceipt) error {
	// See HandleMatrixMessage: no connector gate under the portal event lock.
	if receipt == nil || receipt.Portal == nil || receipt.Portal.Portal == nil {
		return nil
	}
	chatID, err := parseChatID(receipt.Portal.ID)
	if err != nil {
		return nil
	}
	if err = kc.checkSourceAccess(ctx, chatID); err != nil {
		return err
	}
	target := receipt.ExactMessage
	if (target == nil || target.HasFakeMXID()) && !receipt.ReadUpTo.IsZero() {
		target, err = kc.store.lastRealMessageAtOrBefore(ctx, receipt.Portal.PortalKey, receipt.ReadUpTo)
		if err != nil {
			return fmt.Errorf("connector: read receipt target lookup failed")
		}
	}
	if target == nil || target.HasFakeMXID() {
		return nil
	}
	targetChat, logID, err := parseMessageID(target.ID)
	if err != nil || targetChat != chatID {
		return nil
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return bridgev2.ErrNotLoggedIn
	}
	kc.scheduleReadAck(chatID, logID)
	return nil
}

// scheduleReadAck records logID as the chat's acknowledgement position and
// arms the chat's coalescing timer if none is running. Positions only move
// forward; the client also suppresses positions it already acknowledged.
func (kc *KakaoClient) scheduleReadAck(chatID, logID int64) {
	kc.readAckMu.Lock()
	if pending := kc.readAcks[chatID]; pending != nil {
		pending.logID = max(pending.logID, logID)
		kc.readAckMu.Unlock()
		return
	}
	if kc.readAcks == nil {
		kc.readAcks = make(map[int64]*pendingReadAck)
	}
	pending := &pendingReadAck{logID: logID}
	kc.readAcks[chatID] = pending
	kc.readAckMu.Unlock()
	// Armed outside the lock: the timer callback takes it.
	timer := kc.afterFunc(readReceiptCoalesceWindow, func() { kc.flushReadAck(chatID, pending) })
	kc.readAckMu.Lock()
	pending.timer = timer
	kc.readAckMu.Unlock()
}

// flushReadAck sends one chat's pending acknowledgement, unless it was
// already taken by Disconnect. A failure is logged once and not retried,
// matching the shared request path; the next receipt tries again.
func (kc *KakaoClient) flushReadAck(chatID int64, pending *pendingReadAck) {
	kc.readAckMu.Lock()
	if kc.readAcks[chatID] != pending {
		kc.readAckMu.Unlock()
		return
	}
	delete(kc.readAcks, chatID)
	logID := pending.logID
	kc.readAckMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), readAckTimeout)
	defer cancel()
	kc.sendReadAck(ctx, chatID, logID)
}

// flushReadAcks sends every pending acknowledgement now. Disconnect calls it
// before closing the session so a receipt inside its window is not lost.
func (kc *KakaoClient) flushReadAcks(ctx context.Context) {
	kc.readAckMu.Lock()
	pending := kc.readAcks
	kc.readAcks = nil
	kc.readAckMu.Unlock()
	chats := make([]int64, 0, len(pending))
	for chatID, ack := range pending {
		if ack.timer != nil {
			ack.timer.Stop()
		}
		chats = append(chats, chatID)
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i] < chats[j] })
	for _, chatID := range chats {
		kc.sendReadAck(ctx, chatID, pending[chatID].logID)
	}
}

func (kc *KakaoClient) sendReadAck(ctx context.Context, chatID, logID int64) {
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		kc.log().Debug().Int64("kakao_chat_id", chatID).Msg("Dropping Kakao read acknowledgement: not connected")
		return
	}
	if _, err := c.MarkRead(ctx, chatID, logID); err != nil {
		kc.log().Warn().Err(classifyReadReceiptFailure(err)).Int64("kakao_chat_id", chatID).Msg("Kakao read acknowledgement failed")
	}
}

// classifyReadReceiptFailure keeps only stable categories. Backend errors can
// carry endpoints or response detail, so the cause is not wrapped.
func classifyReadReceiptFailure(cause error) error {
	var status client.StatusError
	switch {
	case errors.As(cause, &status):
		return fmt.Errorf("%w: status %d", errReadReceiptMutation, status.Status)
	case errors.Is(cause, context.Canceled):
		return fmt.Errorf("%w: %w", errReadReceiptMutation, context.Canceled)
	case errors.Is(cause, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", errReadReceiptMutation, context.DeadlineExceeded)
	case errors.Is(cause, client.ErrClosed), errors.Is(cause, client.ErrClientClosed):
		return fmt.Errorf("%w: session closed", errReadReceiptMutation)
	default:
		return fmt.Errorf("%w: outcome unknown", errReadReceiptMutation)
	}
}

// applyChatOnRoomWatermarks bridges member reads that happened while the room
// was not viewed. Read notices are live-only; CHATONROOM, sent when the room is
// viewed as the official client does on chat entry, returns every active
// member's watermark and, in owned observation, acknowledged nothing. Each
// watermark passes the same forward-only guard as a live notice.
func (kc *KakaoClient) applyChatOnRoomWatermarks(chatID int64, room chatmeta.ChatOnRoomResponse) {
	members := make([]int64, 0, len(room.Watermarks))
	for member := range room.Watermarks {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	for _, member := range members {
		kc.handleReadState(events.ReadStateChanged{ChatID: chatID, UserID: member, Watermark: room.Watermarks[member]})
	}
}
