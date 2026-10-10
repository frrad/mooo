package client

import (
	"context"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/booking"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// This file holds the CHECKIN dial order and the LOGINLIST/LCHATLIST page
// synchronization that connectSession drives.

type loginCursor struct {
	chatData         map[int64]bson.Raw
	complete         bool
	lastTokenID      *int64
	lbk              *int32
	observed         []continuity.ChatTarget
	deleted          []int64
	replaceInventory bool
}

func checkin(ctx context.Context, hosts []string, ports []int, body []byte, dialers sessionDialers) (loco.Packet, *wireConn, error) {
	for _, host := range hosts {
		for _, port := range append([]int{443}, ports...) {
			wire, err := dialers.tls(ctx, host, port)
			if err != nil {
				continue
			}
			reply, _, requestErr := wire.request(1, booking.CheckinCommand, body)
			_ = wire.close()
			status, statusErr := loco.Status(reply)
			if requestErr == nil && statusErr == nil && status == 0 {
				return reply, wire, nil
			}
		}
		for _, port := range append(append([]int(nil), ports...), 995) {
			wire, err := dialers.secure(ctx, host, port)
			if err != nil {
				continue
			}
			reply, _, requestErr := wire.request(1, booking.CheckinCommand, body)
			_ = wire.close()
			status, statusErr := loco.Status(reply)
			if requestErr == nil && statusErr == nil && status == 0 {
				return reply, wire, nil
			}
		}
	}
	return loco.Packet{}, nil, errBootstrap
}

// finishLoginSyncSession applies the LOGINLIST reply body first and follows
// LCHATLIST until the list ends. It returns every page's chat data in order.
func finishLoginSyncSession(ctx context.Context, session *session, first []byte, beforeRequest func()) ([]bson.Raw, loginCursor, error) {
	body := bson.Raw(first)
	var chats []bson.Raw
	var cursor loginCursor
	for range 20 {
		page, err := sessionlogin.DecodeLoginListPage(body)
		if err != nil {
			return nil, cursor, ErrProtocol
		}
		cursor.apply(page)
		for _, chat := range page.Chats {
			chats = append(chats, chat.Data)
		}
		if page.Next == nil {
			cursor.complete = page.CommitsCursor()
			return chats, cursor, nil
		}
		request, err := page.Next.MarshalBSON()
		if err != nil {
			return nil, cursor, ErrProtocol
		}
		if beforeRequest != nil {
			beforeRequest()
		}
		reply, err := session.requestRaw(ctx, 0, sessionlogin.LChatListCommand, request)
		replyStatus, statusErr := loco.Status(reply)
		if err != nil || statusErr != nil || !sessionlogin.AcceptedLChatListStatus(replyStatus) {
			return nil, cursor, ErrProtocol
		}
		body = reply.Body
	}
	return nil, cursor, ErrProtocol
}

// apply records one page: its deletions, then its chat data, then the global
// cursors when the page commits them. A later page's deletion removes an
// earlier page's chat before session installation.
func (cursor *loginCursor) apply(page sessionlogin.LoginListPage) {
	if page.CommitsCursor() {
		if page.LastTokenID != nil {
			cursor.lastTokenID = page.LastTokenID
		}
		if page.LBK != nil {
			cursor.lbk = page.LBK
		}
	}
	for _, chatID := range page.DeletedChatIDs {
		cursor.deleted = append(cursor.deleted, chatID)
		delete(cursor.chatData, chatID)
		for i, target := range cursor.observed {
			if target.ChatID == chatID {
				cursor.observed = append(cursor.observed[:i], cursor.observed[i+1:]...)
				break
			}
		}
	}
	for _, chat := range page.Chats {
		setLoginTarget(&cursor.observed, continuity.ChatTarget{ChatID: chat.ChatID, MaxLogID: chat.MaxLogID})
		if cursor.chatData == nil {
			cursor.chatData = make(map[int64]bson.Raw)
		}
		cursor.chatData[chat.ChatID] = chat.Data
	}
}

func setLoginTarget(targets *[]continuity.ChatTarget, target continuity.ChatTarget) {
	for i := range *targets {
		if (*targets)[i].ChatID == target.ChatID {
			(*targets)[i] = target
			return
		}
	}
	*targets = append(*targets, target)
}
