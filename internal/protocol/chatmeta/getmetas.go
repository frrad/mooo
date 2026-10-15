package chatmeta

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// GetMetasCommand reads the shared chat metas of several rooms. The official
// Mac client (26.8.0) sends it after the last LCHATLIST page for rooms whose
// chat-data metaMaxRevision exceeds the room's stored meta revision.
const GetMetasCommand = "GETMETAS"

// MaxGetMetasBatch is the official client's per-request chat ID limit.
const MaxGetMetasBatch = 50

// GetMetasRequest carries the chat IDs as int64 cs (Mac LocoGetMetasRequest).
type GetMetasRequest struct{ ChatIDs []int64 }

func (r GetMetasRequest) MarshalBSON() ([]byte, error) {
	if len(r.ChatIDs) == 0 || len(r.ChatIDs) > MaxGetMetasBatch {
		return nil, ErrInvalidRequest
	}
	ids := make(bson.A, 0, len(r.ChatIDs))
	for _, chatID := range r.ChatIDs {
		if chatID <= 0 {
			return nil, ErrInvalidRequest
		}
		ids = append(ids, chatID)
	}
	return bson.Marshal(bson.D{{Key: "cs", Value: ids}})
}

// ChatMetaSet is one room's metas in a GETMETAS reply (wire c, ms).
type ChatMetaSet struct {
	ChatID int64
	Metas  []ChatMeta
}

// DecodeGetMetasResponse decodes mss, the reply's chat meta sets (Mac
// LocoGetMetasResponse). A missing or null mss is an empty reply.
func DecodeGetMetasResponse(body []byte) ([]ChatMetaSet, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return nil, ErrInvalidResponse
	}
	value, ok := fields.Lookup(raw, "mss", "chatMetaSets")
	if !ok {
		return nil, nil
	}
	if value.Type != bson.TypeArray {
		return nil, fmt.Errorf("%w: chat meta sets", ErrInvalidResponse)
	}
	values, err := value.Array().Values()
	if err != nil || len(values) > MaxGetMetasBatch {
		return nil, fmt.Errorf("%w: chat meta sets", ErrInvalidResponse)
	}
	sets := make([]ChatMetaSet, 0, len(values))
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return nil, fmt.Errorf("%w: chat meta set", ErrInvalidResponse)
		}
		document := value.Document()
		var set ChatMetaSet
		if set.ChatID, ok, err = fields.Int64(document, "c", "chatId"); err != nil || !ok || set.ChatID <= 0 {
			return nil, fmt.Errorf("%w: chat meta set chat id", ErrInvalidResponse)
		}
		if set.Metas, err = decodeChatMetas(document, "ms", "chatMetas"); err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	return sets, nil
}
