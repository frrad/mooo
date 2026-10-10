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
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

var errReadReceiptMutation = errors.New("connector: Kakao read acknowledgement failed")

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

func memberReadWatermarkKey(login string, chatID, userID int64) database.Key {
	return database.Key(fmt.Sprintf("kakao:read-watermark:%s:%d:%d", login, chatID, userID))
}

// memberReadWatermark returns the highest watermark bridged for one member,
// or zero when none was recorded.
func (kc *KakaoClient) memberReadWatermark(ctx context.Context, chatID, userID int64) int64 {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return 0
	}
	value, _ := strconv.ParseInt(kc.login.Bridge.DB.KV.Get(ctx, memberReadWatermarkKey(string(kc.login.ID), chatID, userID)), 10, 64)
	return value
}

func (kc *KakaoClient) setMemberReadWatermark(ctx context.Context, chatID, userID, watermark int64) {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return
	}
	kc.login.Bridge.DB.KV.Set(ctx, memberReadWatermarkKey(string(kc.login.ID), chatID, userID), strconv.FormatInt(watermark, 10))
}

// readReceiptTarget returns the bridged message at the watermark or, failing
// that, the latest bridged message of the chat before it. Kakao log IDs
// increase within a chat, so the comparison is by log ID, not timestamp.
func (kc *KakaoClient) readReceiptTarget(ctx context.Context, chatID, watermark int64) (networkid.MessageID, error) {
	exact := makeMessageID(chatID, watermark)
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return exact, nil
	}
	messages := kc.login.Bridge.DB.Message
	row, err := messages.GetLastPartByID(ctx, kc.login.ID, exact)
	if err != nil {
		return "", err
	}
	if row != nil && !row.HasFakeMXID() {
		return exact, nil
	}
	recent, err := messages.GetLastNInPortal(ctx, makePortalKey(chatID, kc.login.ID), readReceiptTargetWindow)
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

// HandleMatrixReadReceipt acknowledges the receipted Kakao message once. The
// client persists each successful acknowledgement, so a repeated receipt is
// not re-sent; a failed one is reported without retrying, matching the shared
// request path.
func (kc *KakaoClient) HandleMatrixReadReceipt(ctx context.Context, receipt *bridgev2.MatrixReadReceipt) error {
	// See HandleMatrixMessage: no connector gate under the portal event lock.
	if receipt == nil || receipt.Portal == nil || receipt.Portal.Portal == nil {
		return nil
	}
	chatID, err := parseChatID(receipt.Portal.ID)
	if err != nil {
		return nil
	}
	if err = kc.checkSourceAccess(ctx, chatID, receipt.Portal); err != nil {
		return err
	}
	target := receipt.ExactMessage
	if (target == nil || target.HasFakeMXID()) && receipt.Portal.Bridge != nil && receipt.Portal.Bridge.DB != nil && !receipt.ReadUpTo.IsZero() {
		target, err = receipt.Portal.Bridge.DB.Message.GetLastNonFakePartAtOrBeforeTime(ctx, receipt.Portal.PortalKey, receipt.ReadUpTo)
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
	if _, err := c.MarkRead(ctx, chatID, logID); err != nil {
		return classifyReadReceiptFailure(err)
	}
	return nil
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

type chatOnRoomReader interface {
	ChatOnRoom(ctx context.Context, chatID int64) (chatmeta.ChatOnRoomResponse, error)
}

// recoverReadWatermarks bridges member reads that happened while the bridge
// was away. Read notices are live-only; CHATONROOM returns every active
// member's watermark and, in owned observation, acknowledged nothing. Each
// watermark passes the same forward-only guard as a live notice. Failures
// are logged and never block the connection.
func (kc *KakaoClient) recoverReadWatermarks(ctx context.Context, c kakaoClient) {
	reader, ok := c.(chatOnRoomReader)
	if !ok || kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return
	}
	portals, err := kc.login.Bridge.DB.Portal.GetAllWithMXID(ctx)
	if err != nil {
		kc.log().Warn().Err(err).Msg("Could not list portals for read recovery")
		return
	}
	for _, portal := range portals {
		if portal.Receiver != kc.login.ID {
			continue
		}
		chatID, err := parseChatID(portal.ID)
		if err != nil || chatID <= 0 || kc.checkSourceAccess(ctx, chatID, nil) != nil {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		room, err := reader.ChatOnRoom(requestCtx, chatID)
		cancel()
		if err != nil || room.ChatID != chatID {
			kc.log().Warn().Int64("kakao_chat_id", chatID).Msg("Read watermark recovery skipped for chat")
			continue
		}
		members := make([]int64, 0, len(room.Watermarks))
		for member := range room.Watermarks {
			members = append(members, member)
		}
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		for _, member := range members {
			kc.handleReadState(events.ReadStateChanged{ChatID: chatID, UserID: member, Watermark: room.Watermarks[member]})
		}
	}
}
