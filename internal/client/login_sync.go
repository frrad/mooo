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

// checkin tries each CHECKIN host and port in the official dial order and
// returns the first accepted reply. When every attempt fails it reports the
// last failure, tagged "checkin dial" or "checkin".
func checkin(ctx context.Context, hosts []string, ports []int, body []byte, dialers sessionDialers) (loco.Packet, *wireConn, error) {
	failure := errBootstrap
	attempt := func(dial wireDialer, host string, port int) (loco.Packet, *wireConn, bool) {
		wire, err := dial(ctx, host, port)
		if err != nil {
			failure = stageErr(errBootstrap, "checkin dial", err)
			return loco.Packet{}, nil, false
		}
		reply, _, requestErr := wire.request(1, booking.CheckinCommand, body)
		_ = wire.close()
		status, statusErr := loco.Status(reply)
		if cause := replyFailure(booking.CheckinCommand, requestErr, statusErr, status); cause != nil {
			failure = stageErr(errBootstrap, "checkin", cause)
			return loco.Packet{}, nil, false
		}
		return reply, wire, true
	}
	for _, host := range hosts {
		for _, port := range append([]int{443}, ports...) {
			if reply, wire, ok := attempt(dialers.tls, host, port); ok {
				return reply, wire, nil
			}
		}
		for _, port := range append(append([]int(nil), ports...), 995) {
			if reply, wire, ok := attempt(dialers.secure, host, port); ok {
				return reply, wire, nil
			}
		}
	}
	return loco.Packet{}, nil, failure
}

// replyFailure returns the first reason a single-reply exchange failed: the
// request error, an unreadable status, or a nonzero status. It is nil when
// the reply was accepted.
func replyFailure(command string, requestErr, statusErr error, status int32) error {
	switch {
	case requestErr != nil:
		return requestErr
	case statusErr != nil:
		return statusErr
	case status != 0:
		return StatusError{Command: command, Status: status}
	}
	return nil
}

// finishLoginSyncSession applies the LOGINLIST reply body first and follows
// LCHATLIST until the list ends. It returns every page's chat data in order.
func finishLoginSyncSession(ctx context.Context, session *session, first []byte, beforeRequest func()) ([]bson.Raw, loginCursor, error) {
	body := bson.Raw(first)
	var chats []bson.Raw
	var cursor loginCursor
	command := "LOGINLIST"
	for range 20 {
		page, err := sessionlogin.DecodeLoginListPage(body)
		if err != nil {
			return nil, cursor, protocolErr(command, "page")
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
			return nil, cursor, protocolErr(sessionlogin.LChatListCommand, "request")
		}
		if beforeRequest != nil {
			beforeRequest()
		}
		command = sessionlogin.LChatListCommand
		reply, err := session.requestRaw(ctx, 0, command, request)
		if err != nil {
			return nil, cursor, stageErr(ErrProtocol, command, err)
		}
		replyStatus, statusErr := loco.Status(reply)
		if statusErr != nil || !sessionlogin.AcceptedLChatListStatus(replyStatus) {
			return nil, cursor, protocolErr(command, "status")
		}
		body = reply.Body
	}
	return nil, cursor, protocolErr(sessionlogin.LChatListCommand, "pages")
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
