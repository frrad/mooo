package client

import (
	"context"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// This file holds the bootstrap target parsing and the LOGINLIST/LCHATLIST
// page synchronization that connectSessionWithResumeOptions drives.

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
			reply, _, requestErr := wire.request(1, "CHECKIN", body)
			_ = wire.close()
			status, statusErr := responseStatus(reply)
			if requestErr == nil && statusErr == nil && status == 0 {
				return reply, wire, nil
			}
		}
		for _, port := range append(append([]int(nil), ports...), 995) {
			wire, err := dialers.secure(ctx, host, port)
			if err != nil {
				continue
			}
			reply, _, requestErr := wire.request(1, "CHECKIN", body)
			_ = wire.close()
			status, statusErr := responseStatus(reply)
			if requestErr == nil && statusErr == nil && status == 0 {
				return reply, wire, nil
			}
		}
	}
	return loco.Packet{}, nil, ErrBootstrap
}

func bookingTargets(body []byte) ([]string, []int, error) {
	raw := bson.Raw(body)
	ticket, err := raw.LookupErr("ticket")
	if err != nil || ticket.Type != bson.TypeEmbeddedDocument {
		return nil, nil, ErrProtocol
	}
	lsl, err := ticket.Document().LookupErr("lsl")
	if err != nil || lsl.Type != bson.TypeArray {
		return nil, nil, ErrProtocol
	}
	values, err := lsl.Array().Values()
	if err != nil {
		return nil, nil, ErrProtocol
	}
	var hosts []string
	for _, value := range values {
		if value.Type == bson.TypeString && value.StringValue() != "" {
			hosts = append(hosts, value.StringValue())
		}
	}
	wifi, err := raw.LookupErr("wifi")
	if err != nil || wifi.Type != bson.TypeEmbeddedDocument {
		return nil, nil, ErrProtocol
	}
	portValue, err := wifi.Document().LookupErr("ports")
	if err != nil || portValue.Type != bson.TypeArray {
		return nil, nil, ErrProtocol
	}
	values, err = portValue.Array().Values()
	if err != nil {
		return nil, nil, ErrProtocol
	}
	var ports []int
	for _, value := range values {
		if value.Type == bson.TypeInt32 {
			port := int(value.Int32())
			if port > 0 && port <= 65535 {
				ports = append(ports, port)
			}
		}
	}
	if len(hosts) == 0 {
		return nil, nil, ErrProtocol
	}
	if len(ports) == 0 {
		ports = []int{995}
	}
	return hosts, ports, nil
}

func endpoint(body []byte) (string, int, error) {
	raw := bson.Raw(body)
	hostValue, err := raw.LookupErr("host")
	if err != nil || hostValue.Type != bson.TypeString || hostValue.StringValue() == "" {
		return "", 0, ErrProtocol
	}
	portValue, err := raw.LookupErr("port")
	if err != nil {
		return "", 0, ErrProtocol
	}
	var port int
	switch portValue.Type {
	case bson.TypeInt32:
		port = int(portValue.Int32())
	case bson.TypeInt64:
		port = int(portValue.Int64())
	default:
		return "", 0, ErrProtocol
	}
	if port <= 0 || port > 65535 {
		return "", 0, ErrProtocol
	}
	return hostValue.StringValue(), port, nil
}

func finishLoginSyncSession(ctx context.Context, session *Session, first []byte, firstStatus int32, beforeRequest func()) ([]bson.Raw, loginCursor, error) {
	page := append(bson.Raw(nil), first...)
	status := firstStatus
	var chats []bson.Raw
	var cursor loginCursor
	for range 20 {
		pageChats, eof, err := parseChatPageContent(page)
		if err != nil {
			return nil, cursor, err
		}
		// The official client applies per-chat deltas for its accepted negative
		// list statuses, but only a status-zero EOF commits global progress.
		if err := updateLoginCursor(page, &cursor, status == 0 && eof); err != nil {
			return nil, cursor, err
		}
		chats = append(chats, pageChats...)
		if status != 0 {
			if status == -305 || status == -310 {
				return chats, cursor, nil
			}
			return nil, cursor, ErrProtocol
		}
		if eof {
			cursor.complete = true
			return chats, cursor, nil
		}
		lastTokenID, err := bsonInt64(page, "lastTokenId")
		if err != nil {
			return nil, cursor, ErrProtocol
		}
		lastChatID, err := bsonInt64(page, "lastChatId")
		if err != nil {
			return nil, cursor, ErrProtocol
		}
		body, err := bson.Marshal(bson.D{
			{Key: "lastTokenId", Value: lastTokenID},
			{Key: "lastChatId", Value: lastChatID},
		})
		if err != nil {
			return nil, cursor, ErrProtocol
		}
		if beforeRequest != nil {
			beforeRequest()
		}
		reply, err := session.requestRaw(ctx, 0, "LCHATLIST", body)
		replyStatus, statusErr := responseStatus(reply)
		if err != nil || statusErr != nil || (replyStatus != 0 && replyStatus != -310) {
			return nil, cursor, ErrProtocol
		}
		status = replyStatus
		page = append(page[:0], reply.Body...)
	}
	return nil, cursor, ErrProtocol
}

func updateLoginCursor(page bson.Raw, cursor *loginCursor, updateGlobal bool) error {
	if updateGlobal {
		if value, err := bsonInt64(page, "lastTokenId"); err == nil && value >= 0 {
			copy := value
			cursor.lastTokenID = &copy
		}
		if value, err := bsonInt64(page, "lbk"); err == nil && value >= 0 && value <= 1<<31-1 {
			copy := int32(value)
			cursor.lbk = &copy
		}
	}
	// Apply each page in official deletion-before-chat-data order. A later
	// deletion must remove an earlier page snapshot before session installation.
	if value, err := page.LookupErr("delChatIds"); err == nil {
		if value.Type != bson.TypeArray {
			return ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return ErrProtocol
		}
		for _, value := range values {
			var chatID int64
			switch value.Type {
			case bson.TypeInt32:
				chatID = int64(value.Int32())
			case bson.TypeInt64:
				chatID = value.Int64()
			default:
				return ErrProtocol
			}
			if chatID <= 0 {
				return ErrProtocol
			}
			cursor.deleted = append(cursor.deleted, chatID)
			delete(cursor.chatData, chatID)
			for i, target := range cursor.observed {
				if target.ChatID == chatID {
					cursor.observed = append(cursor.observed[:i], cursor.observed[i+1:]...)
					break
				}
			}
		}
	}
	if value, err := page.LookupErr("chatDatas"); err == nil {
		if value.Type != bson.TypeArray {
			return ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return ErrProtocol
		}
		for _, value := range values {
			if value.Type != bson.TypeEmbeddedDocument {
				return ErrProtocol
			}
			target, err := loginChatTarget(value.Document())
			if err != nil {
				return err
			}
			setLoginTarget(&cursor.observed, target)
			if cursor.chatData == nil {
				cursor.chatData = make(map[int64]bson.Raw)
			}
			cursor.chatData[target.ChatID] = append(bson.Raw(nil), value.Document()...)
		}
	}
	return nil
}

func loginChatTarget(raw bson.Raw) (continuity.ChatTarget, error) {
	chatID, err := bsonInt64(raw, "c")
	if err != nil || chatID <= 0 {
		return continuity.ChatTarget{}, ErrProtocol
	}
	target := continuity.ChatTarget{ChatID: chatID}
	last, err := raw.LookupErr("l")
	if err != nil || last.Type == bson.TypeNull {
		return target, nil
	}
	if last.Type != bson.TypeEmbeddedDocument {
		return continuity.ChatTarget{}, ErrProtocol
	}
	logID, err := bsonInt64(last.Document(), "logId")
	if err != nil || logID <= 0 {
		return continuity.ChatTarget{}, ErrProtocol
	}
	if nestedChatID, nestedErr := bsonInt64(last.Document(), "chatId"); nestedErr == nil && nestedChatID != chatID {
		return continuity.ChatTarget{}, ErrProtocol
	}
	// l is the last displayable chat log; ll, the last log ID, also covers
	// later feeds such as edits, which catch-up must reach.
	if lastLogID, err := bsonInt64(raw, "ll"); err == nil && lastLogID > logID {
		logID = lastLogID
	}
	target.MaxLogID = logID
	return target, nil
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

func parseChatPageContent(page bson.Raw) ([]bson.Raw, bool, error) {
	if err := page.Validate(); err != nil {
		return nil, false, ErrProtocol
	}
	var chats []bson.Raw
	if value, err := page.LookupErr("chatDatas"); err == nil {
		if value.Type != bson.TypeArray {
			return nil, false, ErrProtocol
		}
		values, err := value.Array().Values()
		if err != nil {
			return nil, false, ErrProtocol
		}
		for _, value := range values {
			if value.Type != bson.TypeEmbeddedDocument {
				return nil, false, ErrProtocol
			}
			chats = append(chats, append(bson.Raw(nil), value.Document()...))
		}
	}
	eofValue, err := page.LookupErr("eof")
	if err != nil || eofValue.Type != bson.TypeBoolean {
		return nil, false, ErrProtocol
	}
	return chats, eofValue.Boolean(), nil
}
