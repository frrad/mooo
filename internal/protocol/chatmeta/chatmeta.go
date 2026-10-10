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

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
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

// fields reads chat metadata. Null and undefined count as absent, so a
// legacy alias after a null current key still applies.
var fields = bsonfield.Reader{Err: ErrInvalidResponse, NullAbsent: true}

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
	value, ok := fields.Lookup(raw, "chatInfo")
	if !ok || value.Type != bson.TypeEmbeddedDocument {
		return ChatInfoResponse{}, fmt.Errorf("%w: chatInfo", ErrInvalidResponse)
	}
	data, err := DecodeChatData(value.Document())
	if err != nil {
		return ChatInfoResponse{}, err
	}
	blind, _, err := fields.Int64s(raw, "bmids")
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

	if data.ChatID, ok, err = fields.Int64(raw, "c", "chatId"); err != nil {
		return ChatData{}, err
	}
	if !ok || data.ChatID <= 0 {
		return ChatData{}, fmt.Errorf("%w: chat id", ErrInvalidResponse)
	}
	if data.Type, _, err = fields.String(raw, "t", "type"); err != nil {
		return ChatData{}, err
	}
	if data.ActiveMemberCount, _, err = fields.Int32(raw, "a", "activeMembersCount", "activeMemberCount"); err != nil {
		return ChatData{}, err
	}
	if data.NewMessageCount, _, err = fields.Int32(raw, "n", "newMessageCount"); err != nil {
		return ChatData{}, err
	}
	if data.LastSeenLogID, _, err = fields.Int64(raw, "s", "lastSeenLogId"); err != nil {
		return ChatData{}, err
	}
	if data.LastServerLogID, _, err = fields.Int64(raw, "ll", "lastServerLogId"); err != nil {
		return ChatData{}, err
	}
	if value, ok := fields.Lookup(raw, "l", "lastChatLog"); ok {
		if value.Type != bson.TypeEmbeddedDocument {
			return ChatData{}, fmt.Errorf("%w: last chat log", ErrInvalidResponse)
		}
		data.LastChatLog = append(bson.Raw(nil), value.Document()...)
	}
	if err := decodeDisplay(raw, &data); err != nil {
		return ChatData{}, err
	}
	if data.PushAlert, data.PushAlertSet, err = fields.Bool(raw, "p", "pushAlert"); err != nil {
		return ChatData{}, err
	}
	if data.Meta, err = decodeRoomMeta(raw); err != nil {
		return ChatData{}, err
	}
	if data.ChatMetas, err = decodeChatMetas(raw); err != nil {
		return ChatData{}, err
	}
	if data.MetaMaxRevision, _, err = fields.Int64(raw, "mmr", "metaMaxRevision"); err != nil {
		return ChatData{}, err
	}
	if data.JoinedAtForNewMem, _, err = fields.Int32(raw, "jn", "joinedAtForNewMem"); err != nil {
		return ChatData{}, err
	}
	if data.InviterID, _, err = fields.Int64(raw, "ii", "inviterId"); err != nil {
		return ChatData{}, err
	}
	if data.LinkID, _, err = fields.Int64(raw, "li", "linkId"); err != nil {
		return ChatData{}, err
	}
	if data.LinkToken, _, err = fields.Int32(raw, "otk", "linkToken"); err != nil {
		return ChatData{}, err
	}
	if data.BlindMemberIDs, _, err = fields.Int64s(raw, "bmids", "blindMemberIds"); err != nil {
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
	if data.DisplayUserIDs, _, err = fields.Int64s(raw, "displayUserIds"); err != nil {
		return err
	}
	if data.DisplayNicknames, _, err = fields.Strings(raw, "displayNicknames"); err != nil {
		return err
	}
	if data.DisplayImageURLs, _, err = fields.Strings(raw, "displayImageUrls"); err != nil {
		return err
	}
	if data.Suspicions, _, err = fields.Strings(raw, "suspicions"); err != nil {
		return err
	}

	members, ok := fields.Lookup(raw, "displayMembers")
	if ok {
		if members.Type != bson.TypeArray {
			return fmt.Errorf("%w: displayMembers", ErrInvalidResponse)
		}
		values, err := members.Array().Values()
		if err != nil {
			return ErrInvalidResponse
		}
		rawType, _, err := fields.String(raw, "type")
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
			id, ok, err := fields.Int64(member, "userId")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: display member user id", ErrInvalidResponse)
			}
			nickname, _, err := fields.String(member, "nickName")
			if err != nil {
				return err
			}
			suspicion, _, err := fields.String(member, "suspicion")
			if err != nil {
				return err
			}
			ids = append(ids, id)
			nicknames = append(nicknames, nickname)
			suspicions = append(suspicions, suspicion)
			if len(values) == 1 {
				image, _, err := fields.String(member, imageKey)
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

	if ids, ok, err := fields.Int64s(raw, "i"); err != nil {
		return err
	} else if ok {
		data.DisplayUserIDs = ids
	}
	if nicknames, ok, err := fields.Strings(raw, "k"); err != nil {
		return err
	} else if ok {
		data.DisplayNicknames = nicknames
	}
	return nil
}

func decodeRoomMeta(raw bson.Raw) (*RoomMeta, error) {
	value, ok := fields.Lookup(raw, "m", "meta")
	if !ok {
		return nil, nil
	}
	var err error
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
			if *field.target, _, err = fields.String(document, field.key); err != nil {
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
		var jsonFields map[string]any
		if err := json.Unmarshal([]byte(value.StringValue()), &jsonFields); err != nil {
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
			switch v := jsonFields[field.key].(type) {
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
			switch v := jsonFields[field.key].(type) {
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
	value, ok := fields.Lookup(raw, key)
	if !ok {
		return false, nil
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
	value, ok := fields.Lookup(raw, "chatMetas")
	if !ok {
		return nil, nil
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
		if meta.Type, _, err = fields.Int32(document, "type"); err != nil {
			return nil, err
		}
		if meta.Revision, _, err = fields.Int64(document, "revision"); err != nil {
			return nil, err
		}
		if meta.AuthorID, _, err = fields.Int64(document, "authorId"); err != nil {
			return nil, err
		}
		if meta.Content, _, err = fields.String(document, "content"); err != nil {
			return nil, err
		}
		if meta.UpdatedAt, _, err = fields.Int64(document, "updatedAt"); err != nil {
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
	if response.ChatID, ok, err = fields.Int64(raw, "chatId"); err != nil {
		return MemberResponse{}, err
	}
	if !ok || response.ChatID <= 0 {
		return MemberResponse{}, fmt.Errorf("%w: member response chat id", ErrInvalidResponse)
	}
	value, ok := fields.Lookup(raw, "members")
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
	if member.UserID, ok, err = fields.Int64(raw, "userId"); err != nil {
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
		if *field.target, _, err = fields.String(raw, field.keys...); err != nil {
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
		if *field.target, _, err = fields.Int32(raw, field.keys...); err != nil {
			return Member{}, err
		}
	}
	if member.ProfileLinkID, _, err = fields.Int64(raw, "pli", "profilelinkId"); err != nil {
		return Member{}, err
	}
	if member.ChannelID, _, err = fields.Int64(raw, "pfId", "channelId"); err != nil {
		return Member{}, err
	}
	if member.Suspended, _, err = fields.Bool(raw, "suspended"); err != nil {
		return Member{}, err
	}
	if member.Memorial, _, err = fields.Bool(raw, "memorial"); err != nil {
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
	if response.Token, _, err = fields.Int64(raw, "token"); err != nil {
		return MemberListResponse{}, err
	}
	if response.Type, _, err = fields.String(raw, "type"); err != nil {
		return MemberListResponse{}, err
	}
	if response.MemberIDs, _, err = fields.Int64s(raw, "memberIds"); err != nil {
		return MemberListResponse{}, err
	}
	return response, nil
}
