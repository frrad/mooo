package client

import (
	"context"
	"errors"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// HistoryPage is uncommitted history from an explicitly selected interval.
// Its cursor belongs to the caller's history journal, never the live checkpoint.
type HistoryPage struct {
	Events   []events.Event
	Next     int64
	Complete bool
}

// ReadHistoryPage sends one zero-held-message SYNCMSG request. It never retries,
// queues live commits, writes read watermarks, or broadens the selected interval.
// The consuming bridge must verify current room access before each call and
// persist progress only after confirming delivery of each returned event.
func (c *Client) ReadHistoryPage(ctx context.Context, chatID, after, through int64, limit int) (HistoryPage, error) {
	request := syncmsg.Request{ChatID: chatID, Cur: after, Max: through, Count: 0}
	if c == nil || ctx == nil || limit < 1 || limit > int(syncmsg.MaxPageSize) {
		return HistoryPage{}, ErrProtocol
	}
	if _, err := request.MarshalBSON(); err != nil {
		return HistoryPage{}, err
	}
	response, err := c.SyncMessages(ctx, request)
	if err != nil {
		return HistoryPage{}, err
	}
	return decodeHistoryPage(request, response, limit)
}

func decodeHistoryPage(request syncmsg.Request, response syncmsg.Response, limit int) (HistoryPage, error) {
	result := HistoryPage{Next: request.Cur}
	for _, raw := range response.ChatLogs {
		logID, err := syncmsg.LogID(raw)
		if err != nil || logID < request.Cur {
			return HistoryPage{}, ErrProtocol
		}
		if logID == request.Cur || logID > request.Max {
			continue
		}
		// A nested room identity, when present, must not redirect historical content.
		if v, err := raw.LookupErr("chatId"); err == nil && v.Type != bson.TypeNull {
			var id int64
			switch v.Type {
			case bson.TypeInt64:
				id = v.Int64()
			case bson.TypeInt32:
				id = int64(v.Int32())
			default:
				return HistoryPage{}, ErrProtocol
			}
			if id != request.ChatID {
				return HistoryPage{}, ErrProtocol
			}
		}
		body, err := bson.Marshal(bson.D{{Key: "chatId", Value: request.ChatID}, {Key: "chatLog", Value: bson.Raw(raw)}})
		if err != nil {
			return HistoryPage{}, ErrProtocol
		}
		event, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
		if err != nil {
			return HistoryPage{}, err
		}
		chatID, position, ok := events.MessagePosition(event)
		if !ok || chatID != request.ChatID || position != logID {
			return HistoryPage{}, ErrProtocol
		}
		if len(result.Events) < limit {
			result.Events = append(result.Events, event)
			result.Next = logID
		}
	}
	if len(result.Events) == 0 {
		return HistoryPage{}, errors.Join(ErrGapUnresolved, errors.New("client: selected history interval unavailable or non-progressing"))
	}
	result.Complete = result.Next == request.Max
	return result, nil
}
