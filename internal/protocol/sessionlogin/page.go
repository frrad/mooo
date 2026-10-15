package sessionlogin

import (
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
)

// LChatListCommand requests the next chat-list page after a LOGINLIST or
// LCHATLIST page whose eof is false.
const LChatListCommand = "LCHATLIST"

// Non-zero list statuses that still process the page's chat delta. Neither
// continues the list or commits the global cursors.
const (
	// StatusLoginListDeltaOnly is a LOGINLIST status accepted as login success.
	StatusLoginListDeltaOnly int32 = -305
	// StatusPartial is a generic partial success; on LCHATLIST it ends the
	// list phase successfully.
	StatusPartial int32 = -310
)

var ErrMalformedPage = errors.New("sessionlogin: malformed chat-list page")

var fields = bsonfield.Reader{Err: ErrMalformedPage}

// LoginListPage is one LOGINLIST or LCHATLIST reply page.
type LoginListPage struct {
	Status int32
	// DeletedChatIDs is delChatIds in wire order. The official client applies
	// a page's deletions before its chat data.
	DeletedChatIDs []int64
	// Chats is chatDatas in wire order.
	Chats []LoginChat
	EOF   bool
	// LastTokenID and LBK are the global resume cursors (lastTokenId, lbk),
	// nil when absent, not an integer, or out of range. Apply them only when
	// CommitsCursor reports true.
	LastTokenID *int64
	LBK         *int32
	// MCMRevision is the account's personal chat-metadata revision
	// (mcmRevision), nil when absent or not a 32-bit integer.
	MCMRevision *int32
	// Next is the LCHATLIST request that continues the list. It is set exactly
	// when Status is 0 and EOF is false.
	Next *LChatListRequest
}

// LoginChat is one chatDatas entry.
type LoginChat struct {
	ChatID int64
	// MaxLogID is the larger of the last chat log's logId (l.logId) and the
	// last log ID (ll), which also covers later feeds such as edits. It is
	// zero when the chat has no last chat log.
	MaxLogID int64
	// Data is an independent copy of the whole chatDatas document.
	Data bson.Raw
}

// CommitsCursor reports whether this page advances the global token and
// blind-token cursors: only a status-zero EOF page does.
func (p LoginListPage) CommitsCursor() bool { return p.Status == 0 && p.EOF }

// AcceptedLChatListStatus reports whether an LCHATLIST reply with this status
// is processed: 0, or StatusPartial, which ends the list phase.
func AcceptedLChatListStatus(status int32) bool { return status == 0 || status == StatusPartial }

// LChatListRequest is the LCHATLIST body.
type LChatListRequest struct {
	LastTokenID int64
	LastChatID  int64
}

func (r LChatListRequest) MarshalBSON() ([]byte, error) {
	return bson.Marshal(bson.D{
		{Key: "lastTokenId", Value: r.LastTokenID},
		{Key: "lastChatId", Value: r.LastChatID},
	})
}

// DecodeLoginListPage decodes a LOGINLIST or LCHATLIST reply body. The status
// must be 0, StatusLoginListDeltaOnly or StatusPartial; which of those a
// command accepts is the caller's check. eof must be a boolean. delChatIds,
// when present, must be an array of positive integers; chatDatas, when
// present, an array of documents each with a positive integer c.
func DecodeLoginListPage(body bson.Raw) (LoginListPage, error) {
	if err := body.Validate(); err != nil {
		return LoginListPage{}, ErrMalformedPage
	}
	status, ok, err := fields.Int32(body, "status")
	if err != nil || !ok {
		return LoginListPage{}, ErrMalformedPage
	}
	if status != 0 && status != StatusLoginListDeltaOnly && status != StatusPartial {
		return LoginListPage{}, fmt.Errorf("%w: status %d", ErrMalformedPage, status)
	}
	page := LoginListPage{Status: status}
	eof, err := body.LookupErr("eof")
	if err != nil || eof.Type != bson.TypeBoolean {
		return LoginListPage{}, ErrMalformedPage
	}
	page.EOF = eof.Boolean()
	if value, err := body.LookupErr("delChatIds"); err == nil {
		if page.DeletedChatIDs, err = decodeDeletedChatIDs(value); err != nil {
			return LoginListPage{}, err
		}
	}
	if value, err := body.LookupErr("chatDatas"); err == nil {
		if page.Chats, err = decodeLoginChats(value); err != nil {
			return LoginListPage{}, err
		}
	}
	if value, ok, err := fields.Int64(body, "lastTokenId"); err == nil && ok && value >= 0 {
		page.LastTokenID = &value
	}
	if value, ok, err := fields.Int64(body, "lbk"); err == nil && ok && value >= 0 && value <= math.MaxInt32 {
		lbk := int32(value)
		page.LBK = &lbk
	}
	if value, err := body.LookupErr("mcmRevision"); err == nil && value.Type == bson.TypeInt32 {
		revision := value.Int32()
		page.MCMRevision = &revision
	}
	if status == 0 && !page.EOF {
		lastTokenID, ok, err := fields.Int64(body, "lastTokenId")
		if err != nil || !ok {
			return LoginListPage{}, ErrMalformedPage
		}
		lastChatID, ok, err := fields.Int64(body, "lastChatId")
		if err != nil || !ok {
			return LoginListPage{}, ErrMalformedPage
		}
		page.Next = &LChatListRequest{LastTokenID: lastTokenID, LastChatID: lastChatID}
	}
	return page, nil
}

func decodeDeletedChatIDs(value bson.RawValue) ([]int64, error) {
	if value.Type != bson.TypeArray {
		return nil, ErrMalformedPage
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, ErrMalformedPage
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		chatID, ok := bsonfield.Int64Value(value)
		if !ok || chatID <= 0 {
			return nil, ErrMalformedPage
		}
		ids = append(ids, chatID)
	}
	return ids, nil
}

func decodeLoginChats(value bson.RawValue) ([]LoginChat, error) {
	if value.Type != bson.TypeArray {
		return nil, ErrMalformedPage
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, ErrMalformedPage
	}
	chats := make([]LoginChat, 0, len(values))
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return nil, ErrMalformedPage
		}
		chat, err := decodeLoginChat(value.Document())
		if err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	return chats, nil
}

func decodeLoginChat(raw bson.Raw) (LoginChat, error) {
	chatID, ok, err := fields.Int64(raw, "c")
	if err != nil || !ok || chatID <= 0 {
		return LoginChat{}, ErrMalformedPage
	}
	chat := LoginChat{ChatID: chatID, Data: append(bson.Raw(nil), raw...)}
	last, err := raw.LookupErr("l")
	if err != nil || last.Type == bson.TypeNull {
		return chat, nil
	}
	if last.Type != bson.TypeEmbeddedDocument {
		return LoginChat{}, ErrMalformedPage
	}
	logID, ok, err := fields.Int64(last.Document(), "logId")
	if err != nil || !ok || logID <= 0 {
		return LoginChat{}, ErrMalformedPage
	}
	// A nested chatId of the wrong type is ignored, not rejected.
	if nestedChatID, ok, nestedErr := fields.Int64(last.Document(), "chatId"); nestedErr == nil && ok && nestedChatID != chatID {
		return LoginChat{}, ErrMalformedPage
	}
	if lastLogID, ok, err := fields.Int64(raw, "ll"); err == nil && ok && lastLogID > logID {
		logID = lastLogID
	}
	chat.MaxLogID = logID
	return chat, nil
}
