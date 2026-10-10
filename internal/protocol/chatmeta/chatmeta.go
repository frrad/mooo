// Package chatmeta implements the reviewed, transport-independent chat
// metadata contracts: CHATINFO room metadata, MEMBER profile resolution, and
// the MEMLIST roster refresh. See research/chat-metadata.md.
//
// Decoding follows the official model rules where they are proven: a mapped
// short wire key wins over the long property name, and a null value is
// equivalent to absence. The official client coerces or raises on wrong wire
// types; this package instead fails closed with ErrInvalidResponse.
package chatmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	ChatInfoCommand   = "CHATINFO"
	MemberCommand     = "MEMBER"
	MemberListCommand = "MEMLIST"

	// MaxMemberBatch is the official client's MEMBER request batch size.
	MaxMemberBatch = 500
)

var (
	ErrInvalidRequest  = errors.New("chatmeta: invalid request")
	ErrInvalidResponse = errors.New("chatmeta: invalid response")
)

// ChatInfoRequest asks for one room's chat data.
type ChatInfoRequest struct {
	ChatID int64
}

func (r ChatInfoRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidRequest
	}
	return bson.Marshal(bson.D{{Key: "chatId", Value: r.ChatID}})
}

// ChatInfoResponse is a successful CHATINFO reply. BlindMemberIDs is nil when
// bmids is absent or null and non-nil (possibly empty) when present; the
// official client replaces the room's blinded members only in the latter case.
type ChatInfoResponse struct {
	ChatData       ChatData
	BlindMemberIDs []int64
}

// ChatData is the chat-data object shared by CHATINFO and LOGINLIST/LCHATLIST
// chatDatas entries.
type ChatData struct {
	ChatID            int64
	Type              string
	ActiveMemberCount int32
	NewMessageCount   int32
	LastSeenLogID     int64
	LastServerLogID   int64
	// LastChatLog is the raw chat-log document, left for the message decoders.
	LastChatLog      bson.Raw
	DisplayUserIDs   []int64
	DisplayNicknames []string
	DisplayImageURLs []string
	Suspicions       []string
	PushAlert        bool
	// PushAlertSet reports whether the response carried pushAlert.
	PushAlertSet      bool
	Meta              *RoomMeta
	ChatMetas         []ChatMeta
	MetaMaxRevision   int64
	JoinedAtForNewMem int32
	InviterID         int64
	LinkID            int64
	LinkToken         int32
	BlindMemberIDs    []int64
}

// RoomMeta is the room-meta object carried under m.
type RoomMeta struct {
	Name         string
	ImageURL     string
	FullImageURL string
	Favorite     bool
	ChatHide     bool
	ChatCategory string
}

// Shared metadata types recovered from Android 26.8.2 ChatSharedMeta.kt.
// Values are explicit wire codes, not ordinals. Mac 26.8.0 additionally
// consumes 11/12 as Live Talk info/member metadata; Android omits them.
// Android names retain its enum spelling; Live Talk names describe Mac
// projections rather than recovered enum symbols. See research/chat-metadata.md.
const (
	SharedMetaNone                   int32 = 0
	SharedMetaNotice                 int32 = 1
	SharedMetaKakaoGroup             int32 = 2
	SharedMetaTitle                  int32 = 3
	SharedMetaProfile                int32 = 4
	SharedMetaTv                     int32 = 5
	SharedMetaPrivilege              int32 = 6
	SharedMetaTvLive                 int32 = 7
	SharedMetaPlustChatBackground    int32 = 8
	SharedMetaDailyCard              int32 = 9
	SharedMetaDailyCardProfile       int32 = 10
	SharedMetaLiveTalkInfo           int32 = 11 // Mac liveTalkInfo projection.
	SharedMetaLiveTalkMember         int32 = 12 // Mac liveTalkMebmer projection.
	SharedMetaOpenLinkChannelChat    int32 = 13
	SharedMetaOpenLinkBotCommand     int32 = 14
	SharedMetaWarehouse              int32 = 15
	SharedMetaVoiceroom              int32 = 16
	SharedMetaVoiceroomCount         int32 = 17
	SharedMetaCecall                 int32 = 18
	SharedMetaCecallCount            int32 = 19
	SharedMetaOpenLinkChatBackground int32 = 20
	SharedMetaChatBot                int32 = 21
	SharedMetaWebBanner              int32 = 22
)

// ChatMeta is one element of chatMetas. Unknown type values are preserved.
type ChatMeta struct {
	Type      int32
	Revision  int64
	AuthorID  int64
	Content   string
	UpdatedAt int64
}

// DecodeChatInfoResponse decodes a CHATINFO reply body. Status is handled by
// the shared carriage request path.
func DecodeChatInfoResponse(body []byte) (ChatInfoResponse, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return ChatInfoResponse{}, ErrInvalidResponse
	}
	value, ok, err := lookup(raw, "chatInfo")
	if err != nil {
		return ChatInfoResponse{}, err
	}
	if !ok || value.Type != bson.TypeEmbeddedDocument {
		return ChatInfoResponse{}, fmt.Errorf("%w: chatInfo", ErrInvalidResponse)
	}
	data, err := DecodeChatData(value.Document())
	if err != nil {
		return ChatInfoResponse{}, err
	}
	blind, _, err := int64Array(raw, "bmids")
	if err != nil {
		return ChatInfoResponse{}, err
	}
	return ChatInfoResponse{ChatData: data, BlindMemberIDs: blind}, nil
}

// DecodeChatData decodes one chat-data document. The chat ID is required and
// positive; every other field defaults to its zero value when absent or null.
func DecodeChatData(raw bson.Raw) (ChatData, error) {
	if err := raw.Validate(); err != nil {
		return ChatData{}, ErrInvalidResponse
	}
	var data ChatData
	var ok bool
	var err error

	if data.ChatID, ok, err = int64Field(raw, "c", "chatId"); err != nil {
		return ChatData{}, err
	}
	if !ok || data.ChatID <= 0 {
		return ChatData{}, fmt.Errorf("%w: chat id", ErrInvalidResponse)
	}
	if data.Type, _, err = stringField(raw, "t", "type"); err != nil {
		return ChatData{}, err
	}
	if data.ActiveMemberCount, _, err = int32Field(raw, "a", "activeMembersCount", "activeMemberCount"); err != nil {
		return ChatData{}, err
	}
	if data.NewMessageCount, _, err = int32Field(raw, "n", "newMessageCount"); err != nil {
		return ChatData{}, err
	}
	if data.LastSeenLogID, _, err = int64Field(raw, "s", "lastSeenLogId"); err != nil {
		return ChatData{}, err
	}
	if data.LastServerLogID, _, err = int64Field(raw, "ll", "lastServerLogId"); err != nil {
		return ChatData{}, err
	}
	if value, ok, err := lookup(raw, "l", "lastChatLog"); err != nil {
		return ChatData{}, err
	} else if ok {
		if value.Type != bson.TypeEmbeddedDocument {
			return ChatData{}, fmt.Errorf("%w: last chat log", ErrInvalidResponse)
		}
		data.LastChatLog = append(bson.Raw(nil), value.Document()...)
	}
	if err := decodeDisplay(raw, &data); err != nil {
		return ChatData{}, err
	}
	if data.PushAlert, data.PushAlertSet, err = boolField(raw, "p", "pushAlert"); err != nil {
		return ChatData{}, err
	}
	if data.Meta, err = decodeRoomMeta(raw); err != nil {
		return ChatData{}, err
	}
	if data.ChatMetas, err = decodeChatMetas(raw); err != nil {
		return ChatData{}, err
	}
	if data.MetaMaxRevision, _, err = int64Field(raw, "mmr", "metaMaxRevision"); err != nil {
		return ChatData{}, err
	}
	if data.JoinedAtForNewMem, _, err = int32Field(raw, "jn", "joinedAtForNewMem"); err != nil {
		return ChatData{}, err
	}
	if data.InviterID, _, err = int64Field(raw, "ii", "inviterId"); err != nil {
		return ChatData{}, err
	}
	if data.LinkID, _, err = int64Field(raw, "li", "linkId"); err != nil {
		return ChatData{}, err
	}
	if data.LinkToken, _, err = int32Field(raw, "otk", "linkToken"); err != nil {
		return ChatData{}, err
	}
	if data.BlindMemberIDs, _, err = int64Array(raw, "bmids", "blindMemberIds"); err != nil {
		return ChatData{}, err
	}
	return data, nil
}

// decodeDisplay applies the official display-member derivation. Values
// derived from displayMembers replace the long property names, and the mapped
// short keys i and k replace both. Image URLs are derived only for exactly one
// display member, from pi for open rooms (raw type OD or OM) and from
// profileImageUrl otherwise.
func decodeDisplay(raw bson.Raw, data *ChatData) error {
	var err error
	if data.DisplayUserIDs, _, err = int64Array(raw, "displayUserIds"); err != nil {
		return err
	}
	if data.DisplayNicknames, _, err = stringArray(raw, "displayNicknames"); err != nil {
		return err
	}
	if data.DisplayImageURLs, _, err = stringArray(raw, "displayImageUrls"); err != nil {
		return err
	}
	if data.Suspicions, _, err = stringArray(raw, "suspicions"); err != nil {
		return err
	}

	members, ok, err := lookup(raw, "displayMembers")
	if err != nil {
		return err
	}
	if ok {
		if members.Type != bson.TypeArray {
			return fmt.Errorf("%w: displayMembers", ErrInvalidResponse)
		}
		values, err := members.Array().Values()
		if err != nil {
			return ErrInvalidResponse
		}
		rawType, _, err := stringField(raw, "type")
		if err != nil {
			return err
		}
		imageKey := "profileImageUrl"
		if rawType == "OD" || rawType == "OM" {
			imageKey = "pi"
		}
		ids := make([]int64, 0, len(values))
		nicknames := make([]string, 0, len(values))
		suspicions := make([]string, 0, len(values))
		var images []string
		for _, value := range values {
			if value.Type != bson.TypeEmbeddedDocument {
				return fmt.Errorf("%w: display member", ErrInvalidResponse)
			}
			member := value.Document()
			id, ok, err := int64Field(member, "userId")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: display member user id", ErrInvalidResponse)
			}
			nickname, _, err := stringField(member, "nickName")
			if err != nil {
				return err
			}
			suspicion, _, err := stringField(member, "suspicion")
			if err != nil {
				return err
			}
			ids = append(ids, id)
			nicknames = append(nicknames, nickname)
			suspicions = append(suspicions, suspicion)
			if len(values) == 1 {
				image, _, err := stringField(member, imageKey)
				if err != nil {
					return err
				}
				images = []string{image}
			}
		}
		data.DisplayUserIDs = ids
		data.DisplayNicknames = nicknames
		data.Suspicions = suspicions
		if images != nil {
			data.DisplayImageURLs = images
		}
	}

	if ids, ok, err := int64Array(raw, "i"); err != nil {
		return err
	} else if ok {
		data.DisplayUserIDs = ids
	}
	if nicknames, ok, err := stringArray(raw, "k"); err != nil {
		return err
	} else if ok {
		data.DisplayNicknames = nicknames
	}
	return nil
}

func decodeRoomMeta(raw bson.Raw) (*RoomMeta, error) {
	value, ok, err := lookup(raw, "m", "meta")
	if err != nil || !ok {
		return nil, err
	}
	switch value.Type {
	case bson.TypeEmbeddedDocument:
		document := value.Document()
		var meta RoomMeta
		for _, field := range []struct {
			key    string
			target *string
		}{
			{"name", &meta.Name},
			{"imageUrl", &meta.ImageURL},
			{"fullImageUrl", &meta.FullImageURL},
			{"chat_category", &meta.ChatCategory},
		} {
			if *field.target, _, err = stringField(document, field.key); err != nil {
				return nil, err
			}
		}
		if meta.Favorite, err = metaBool(document, "favorite"); err != nil {
			return nil, err
		}
		if meta.ChatHide, err = metaBool(document, "chat_hide"); err != nil {
			return nil, err
		}
		return &meta, nil
	case bson.TypeString:
		var fields map[string]any
		if err := json.Unmarshal([]byte(value.StringValue()), &fields); err != nil {
			return nil, fmt.Errorf("%w: meta JSON", ErrInvalidResponse)
		}
		var meta RoomMeta
		for _, field := range []struct {
			key    string
			target *string
		}{
			{"name", &meta.Name},
			{"imageUrl", &meta.ImageURL},
			{"fullImageUrl", &meta.FullImageURL},
			{"chat_category", &meta.ChatCategory},
		} {
			switch v := fields[field.key].(type) {
			case nil:
			case string:
				*field.target = v
			default:
				return nil, fmt.Errorf("%w: meta %s", ErrInvalidResponse, field.key)
			}
		}
		for _, field := range []struct {
			key    string
			target *bool
		}{
			{"favorite", &meta.Favorite},
			{"chat_hide", &meta.ChatHide},
		} {
			switch v := fields[field.key].(type) {
			case nil:
			case bool:
				*field.target = v
			case string:
				*field.target = v == "true"
			default:
				return nil, fmt.Errorf("%w: meta %s", ErrInvalidResponse, field.key)
			}
		}
		return &meta, nil
	default:
		return nil, fmt.Errorf("%w: meta", ErrInvalidResponse)
	}
}

// metaBool accepts a boolean or the official client's string form, where
// only "true" is true.
func metaBool(raw bson.Raw, key string) (bool, error) {
	value, ok, err := lookup(raw, key)
	if err != nil || !ok {
		return false, err
	}
	switch value.Type {
	case bson.TypeBoolean:
		return value.Boolean(), nil
	case bson.TypeString:
		return value.StringValue() == "true", nil
	default:
		return false, fmt.Errorf("%w: %s", ErrInvalidResponse, key)
	}
}

func decodeChatMetas(raw bson.Raw) ([]ChatMeta, error) {
	value, ok, err := lookup(raw, "chatMetas")
	if err != nil || !ok {
		return nil, err
	}
	if value.Type != bson.TypeArray {
		return nil, fmt.Errorf("%w: chatMetas", ErrInvalidResponse)
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, ErrInvalidResponse
	}
	metas := make([]ChatMeta, 0, len(values))
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return nil, fmt.Errorf("%w: chatMetas element", ErrInvalidResponse)
		}
		document := value.Document()
		var meta ChatMeta
		if meta.Type, _, err = int32Field(document, "type"); err != nil {
			return nil, err
		}
		if meta.Revision, _, err = int64Field(document, "revision"); err != nil {
			return nil, err
		}
		if meta.AuthorID, _, err = int64Field(document, "authorId"); err != nil {
			return nil, err
		}
		if meta.Content, _, err = stringField(document, "content"); err != nil {
			return nil, err
		}
		if meta.UpdatedAt, _, err = int64Field(document, "updatedAt"); err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

// MemberRequest asks for up to MaxMemberBatch member profiles in one room.
// Callers are responsible for the official filtering and batching; see the
// client package.
type MemberRequest struct {
	ChatID    int64
	MemberIDs []int64
}

func (r MemberRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || len(r.MemberIDs) == 0 || len(r.MemberIDs) > MaxMemberBatch {
		return nil, ErrInvalidRequest
	}
	ids := make(bson.A, 0, len(r.MemberIDs))
	for _, id := range r.MemberIDs {
		if id <= 0 {
			return nil, ErrInvalidRequest
		}
		ids = append(ids, id)
	}
	return bson.Marshal(bson.D{
		{Key: "chatId", Value: r.ChatID},
		{Key: "memberIds", Value: ids},
	})
}

// MemberResponse is a successful MEMBER reply. The official client attaches
// members to the room named by this response ChatID, not the request's.
type MemberResponse struct {
	ChatID  int64
	Members []Member
}

// Member is one MEMBER profile. It is distinct from the three-field feed
// member carried by NEWMEM and DELMEM, whose user type is keyed userType.
type Member struct {
	UserID                  int64
	Nickname                string
	ProfileImageURL         string
	FullProfileImageURL     string
	OriginalProfileImageURL string
	Type                    int32
	UserType                int32
	AccountID               int32
	LinkedServices          string
	StatusMessage           string
	CountryISO              string
	Suspended               bool
	Memorial                bool
	AccessPermit            string
	Suspicion               string
	ProfileLinkID           int64
	OpenLinkMemberType      int32
	ChannelID               int64
}

func DecodeMemberResponse(body []byte) (MemberResponse, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return MemberResponse{}, ErrInvalidResponse
	}
	var response MemberResponse
	var err error
	var ok bool
	if response.ChatID, ok, err = int64Field(raw, "chatId"); err != nil {
		return MemberResponse{}, err
	}
	if !ok || response.ChatID <= 0 {
		return MemberResponse{}, fmt.Errorf("%w: member response chat id", ErrInvalidResponse)
	}
	value, ok, err := lookup(raw, "members")
	if err != nil {
		return MemberResponse{}, err
	}
	if !ok {
		return response, nil
	}
	if value.Type != bson.TypeArray {
		return MemberResponse{}, fmt.Errorf("%w: members", ErrInvalidResponse)
	}
	values, err := value.Array().Values()
	if err != nil {
		return MemberResponse{}, ErrInvalidResponse
	}
	response.Members = make([]Member, 0, len(values))
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return MemberResponse{}, fmt.Errorf("%w: member", ErrInvalidResponse)
		}
		member, err := DecodeMember(value.Document())
		if err != nil {
			return MemberResponse{}, err
		}
		response.Members = append(response.Members, member)
	}
	return response, nil
}

// DecodeMember decodes one MEMBER profile object. The user ID is required and
// positive.
func DecodeMember(raw bson.Raw) (Member, error) {
	var member Member
	var ok bool
	var err error
	if member.UserID, ok, err = int64Field(raw, "userId"); err != nil {
		return Member{}, err
	}
	if !ok || member.UserID <= 0 {
		return Member{}, fmt.Errorf("%w: member user id", ErrInvalidResponse)
	}
	for _, field := range []struct {
		target *string
		keys   []string
	}{
		{&member.Nickname, []string{"nickName"}},
		{&member.ProfileImageURL, []string{"pi", "profileImageUrl"}},
		{&member.FullProfileImageURL, []string{"fpi", "fullProfileImageUrl"}},
		{&member.OriginalProfileImageURL, []string{"opi", "originalProfileImageUrl"}},
		{&member.LinkedServices, []string{"linkedServices"}},
		{&member.StatusMessage, []string{"statusMessage"}},
		{&member.CountryISO, []string{"countryIso"}},
		{&member.AccessPermit, []string{"accessPermit"}},
		{&member.Suspicion, []string{"suspicion"}},
	} {
		if *field.target, _, err = stringField(raw, field.keys...); err != nil {
			return Member{}, err
		}
	}
	for _, field := range []struct {
		target *int32
		keys   []string
	}{
		{&member.Type, []string{"type"}},
		{&member.UserType, []string{"ut", "userType"}},
		{&member.AccountID, []string{"accountId"}},
		{&member.OpenLinkMemberType, []string{"mt", "openLinkUserMemberType"}},
	} {
		if *field.target, _, err = int32Field(raw, field.keys...); err != nil {
			return Member{}, err
		}
	}
	if member.ProfileLinkID, _, err = int64Field(raw, "pli", "profilelinkId"); err != nil {
		return Member{}, err
	}
	if member.ChannelID, _, err = int64Field(raw, "pfId", "channelId"); err != nil {
		return Member{}, err
	}
	if member.Suspended, _, err = boolField(raw, "suspended"); err != nil {
		return Member{}, err
	}
	if member.Memorial, _, err = boolField(raw, "memorial"); err != nil {
		return Member{}, err
	}
	return member, nil
}

// MemberListRequest refreshes a room's member-ID roster. Token is the room's
// stored member-list token; its source is an open question, so zero is valid.
type MemberListRequest struct {
	ChatID int64
	Token  int64
}

func (r MemberListRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidRequest
	}
	return bson.Marshal(bson.D{
		{Key: "chatId", Value: r.ChatID},
		{Key: "token", Value: r.Token},
	})
}

type MemberListResponse struct {
	Token     int64
	Type      string
	MemberIDs []int64
}

func DecodeMemberListResponse(body []byte) (MemberListResponse, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return MemberListResponse{}, ErrInvalidResponse
	}
	var response MemberListResponse
	var err error
	if response.Token, _, err = int64Field(raw, "token"); err != nil {
		return MemberListResponse{}, err
	}
	if response.Type, _, err = stringField(raw, "type"); err != nil {
		return MemberListResponse{}, err
	}
	if response.MemberIDs, _, err = int64Array(raw, "memberIds"); err != nil {
		return MemberListResponse{}, err
	}
	return response, nil
}

// lookup returns the value of the first key that is present and not null.
// Documents are validated before lookup, so a lookup error means absence.
func lookup(raw bson.Raw, keys ...string) (bson.RawValue, bool, error) {
	for _, key := range keys {
		value, err := raw.LookupErr(key)
		if err != nil || value.Type == bson.TypeNull || value.Type == bson.TypeUndefined {
			continue
		}
		return value, true, nil
	}
	return bson.RawValue{}, false, nil
}

func integer(value bson.RawValue) (int64, bool) {
	switch value.Type {
	case bson.TypeInt32:
		return int64(value.Int32()), true
	case bson.TypeInt64:
		return value.Int64(), true
	default:
		return 0, false
	}
}

func int64Field(raw bson.Raw, keys ...string) (int64, bool, error) {
	value, ok, err := lookup(raw, keys...)
	if err != nil || !ok {
		return 0, false, err
	}
	n, ok := integer(value)
	if !ok {
		return 0, false, fmt.Errorf("%w: %s", ErrInvalidResponse, keys[0])
	}
	return n, true, nil
}

func int32Field(raw bson.Raw, keys ...string) (int32, bool, error) {
	n, ok, err := int64Field(raw, keys...)
	if err != nil || !ok {
		return 0, ok, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, false, fmt.Errorf("%w: %s out of int32 range", ErrInvalidResponse, keys[0])
	}
	return int32(n), true, nil
}

func stringField(raw bson.Raw, keys ...string) (string, bool, error) {
	value, ok, err := lookup(raw, keys...)
	if err != nil || !ok {
		return "", false, err
	}
	if value.Type != bson.TypeString {
		return "", false, fmt.Errorf("%w: %s", ErrInvalidResponse, keys[0])
	}
	return value.StringValue(), true, nil
}

func boolField(raw bson.Raw, keys ...string) (bool, bool, error) {
	value, ok, err := lookup(raw, keys...)
	if err != nil || !ok {
		return false, false, err
	}
	if value.Type != bson.TypeBoolean {
		return false, false, fmt.Errorf("%w: %s", ErrInvalidResponse, keys[0])
	}
	return value.Boolean(), true, nil
}

func int64Array(raw bson.Raw, keys ...string) ([]int64, bool, error) {
	value, ok, err := lookup(raw, keys...)
	if err != nil || !ok {
		return nil, false, err
	}
	if value.Type != bson.TypeArray {
		return nil, false, fmt.Errorf("%w: %s", ErrInvalidResponse, keys[0])
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, false, ErrInvalidResponse
	}
	result := make([]int64, 0, len(values))
	for _, element := range values {
		n, ok := integer(element)
		if !ok {
			return nil, false, fmt.Errorf("%w: %s element", ErrInvalidResponse, keys[0])
		}
		result = append(result, n)
	}
	return result, true, nil
}

func stringArray(raw bson.Raw, keys ...string) ([]string, bool, error) {
	value, ok, err := lookup(raw, keys...)
	if err != nil || !ok {
		return nil, false, err
	}
	if value.Type != bson.TypeArray {
		return nil, false, fmt.Errorf("%w: %s", ErrInvalidResponse, keys[0])
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, false, ErrInvalidResponse
	}
	result := make([]string, 0, len(values))
	for _, element := range values {
		if element.Type != bson.TypeString {
			return nil, false, fmt.Errorf("%w: %s element", ErrInvalidResponse, keys[0])
		}
		result = append(result, element.StringValue())
	}
	return result, true, nil
}
