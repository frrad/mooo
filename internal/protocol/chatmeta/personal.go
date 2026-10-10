package chatmeta

import "go.mongodb.org/mongo-driver/v2/bson"

const PersonalMetaCommand = "GETMCMETA"

// PersonalMetaRequest reads account-wide personal room settings. The official
// request has no fields; it does not mutate names, images or read watermarks.
type PersonalMetaRequest struct{}

func (PersonalMetaRequest) MarshalBSON() ([]byte, error) { return bson.Marshal(bson.D{}) }

// PersonalMetaResponse pairs each chat ID with the metadata at the same index.
// An absent chat ID is unknown, not evidence that its personal settings cleared.
type PersonalMetaResponse struct{ Rooms map[int64]RoomMeta }

func DecodePersonalMetaResponse(body []byte) (PersonalMetaResponse, error) {
	raw := bson.Raw(body)
	if len(body) > 4<<20 || raw.Validate() != nil {
		return PersonalMetaResponse{}, ErrInvalidResponse
	}
	ids, present, err := int64Array(raw, "chatIds")
	if err != nil || !present || len(ids) > 10000 {
		return PersonalMetaResponse{}, ErrInvalidResponse
	}
	value, present, err := lookup(raw, "metas")
	if err != nil || !present || value.Type != bson.TypeArray {
		return PersonalMetaResponse{}, ErrInvalidResponse
	}
	values, err := value.Array().Values()
	if err != nil || len(values) != len(ids) {
		return PersonalMetaResponse{}, ErrInvalidResponse
	}
	out := PersonalMetaResponse{Rooms: make(map[int64]RoomMeta, len(ids))}
	for i, chatID := range ids {
		if chatID <= 0 {
			return PersonalMetaResponse{}, ErrInvalidResponse
		}
		if _, exists := out.Rooms[chatID]; exists {
			return PersonalMetaResponse{}, ErrInvalidResponse
		}
		if values[i].Type != bson.TypeEmbeddedDocument && values[i].Type != bson.TypeString {
			return PersonalMetaResponse{}, ErrInvalidResponse
		}
		wrapper, err := bson.Marshal(bson.D{{Key: "m", Value: values[i]}})
		if err != nil {
			return PersonalMetaResponse{}, ErrInvalidResponse
		}
		meta, err := decodeRoomMeta(wrapper)
		if err != nil || meta == nil {
			return PersonalMetaResponse{}, ErrInvalidResponse
		}
		out.Rooms[chatID] = *meta
	}
	return out, nil
}
