package chatmeta

import (
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func mustMarshal(t *testing.T, document bson.D) []byte {
	t.Helper()
	body, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestChatInfoRequestEncodesOnlyInt64ChatID(t *testing.T) {
	body, err := ChatInfoRequest{ChatID: 42}.MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	elements, err := bson.Raw(body).Elements()
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 1 || elements[0].Key() != "chatId" {
		t.Fatalf("elements = %v, want only chatId", elements)
	}
	if value := elements[0].Value(); value.Type != bson.TypeInt64 || value.Int64() != 42 {
		t.Fatalf("chatId = %v, want int64(42)", value)
	}
}

func TestChatInfoRequestRejectsNonPositiveChatID(t *testing.T) {
	for _, chatID := range []int64{0, -1} {
		if _, err := (ChatInfoRequest{ChatID: chatID}).MarshalBSON(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("chatId %d: error = %v, want ErrInvalidRequest", chatID, err)
		}
	}
}

func TestDecodeChatInfoResponseReadsShortKeys(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "chatInfo", Value: bson.D{
			{Key: "c", Value: int64(42)},
			{Key: "t", Value: "MultiChat"},
			{Key: "a", Value: int32(3)},
			{Key: "n", Value: int32(5)},
			{Key: "s", Value: int64(100)},
			{Key: "ll", Value: int64(120)},
			{Key: "l", Value: bson.D{{Key: "logId", Value: int64(120)}, {Key: "chatId", Value: int64(42)}}},
			{Key: "i", Value: bson.A{int64(7), int32(8)}},
			{Key: "k", Value: bson.A{"Seven", "Eight"}},
			{Key: "p", Value: true},
			{Key: "m", Value: bson.D{
				{Key: "name", Value: "Synthetic Room"},
				{Key: "imageUrl", Value: "https://example.invalid/room.jpg"},
				{Key: "fullImageUrl", Value: "https://example.invalid/room-full.jpg"},
				{Key: "favorite", Value: true},
				{Key: "chat_hide", Value: false},
				{Key: "chat_category", Value: "synthetic"},
			}},
			{Key: "chatMetas", Value: bson.A{
				bson.D{
					{Key: "type", Value: int32(3)},
					{Key: "revision", Value: int64(2)},
					{Key: "authorId", Value: int64(7)},
					{Key: "content", Value: "synthetic notice"},
					{Key: "updatedAt", Value: int64(1700000000)},
				},
			}},
			{Key: "mmr", Value: int64(9)},
			{Key: "jn", Value: int32(11)},
			{Key: "ii", Value: int64(7)},
			{Key: "li", Value: int64(0)},
			{Key: "otk", Value: int32(0)},
		}},
		{Key: "bmids", Value: bson.A{int64(99)}},
	})

	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	want := ChatData{
		ChatID:            42,
		Type:              "MultiChat",
		ActiveMemberCount: 3,
		NewMessageCount:   5,
		LastSeenLogID:     100,
		LastServerLogID:   120,
		DisplayUserIDs:    []int64{7, 8},
		DisplayNicknames:  []string{"Seven", "Eight"},
		PushAlert:         true,
		Meta: &RoomMeta{
			Name:         "Synthetic Room",
			ImageURL:     "https://example.invalid/room.jpg",
			FullImageURL: "https://example.invalid/room-full.jpg",
			Favorite:     true,
			ChatHide:     false,
			ChatCategory: "synthetic",
		},
		ChatMetas: []ChatMeta{
			{Type: 3, Revision: 2, AuthorID: 7, Content: "synthetic notice", UpdatedAt: 1700000000},
		},
		MetaMaxRevision:   9,
		JoinedAtForNewMem: 11,
		InviterID:         7,
	}
	got := response.ChatData
	if got.LastChatLog == nil {
		t.Fatal("LastChatLog is nil, want the raw l document")
	}
	got.LastChatLog = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChatData =\n%#v\nwant\n%#v", got, want)
	}
	if !reflect.DeepEqual(response.BlindMemberIDs, []int64{99}) {
		t.Fatalf("BlindMemberIDs = %v, want [99]", response.BlindMemberIDs)
	}
}

func TestDecodeChatInfoResponseAcceptsLongPropertyNames(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "type", Value: "DirectChat"},
			{Key: "activeMembersCount", Value: int32(2)},
			{Key: "linkId", Value: int64(0)},
		}},
	})
	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ChatData.ChatID != 42 || response.ChatData.Type != "DirectChat" || response.ChatData.ActiveMemberCount != 2 {
		t.Fatalf("ChatData = %#v", response.ChatData)
	}
}

func TestDecodeChatInfoResponseShortKeyWinsOverPropertyName(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{
			{Key: "chatId", Value: int64(1)},
			{Key: "c", Value: int64(42)},
		}},
	})
	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ChatData.ChatID != 42 {
		t.Fatalf("ChatID = %d, want the mapped c value 42", response.ChatData.ChatID)
	}
}

func TestDecodeChatInfoResponseTreatsNullAsAbsent(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{
			{Key: "c", Value: int64(42)},
			{Key: "t", Value: nil},
			{Key: "i", Value: nil},
			{Key: "m", Value: nil},
			{Key: "l", Value: nil},
		}},
		{Key: "bmids", Value: nil},
	})
	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	want := ChatData{ChatID: 42}
	if !reflect.DeepEqual(response.ChatData, want) {
		t.Fatalf("ChatData = %#v, want %#v", response.ChatData, want)
	}
	if response.BlindMemberIDs != nil {
		t.Fatalf("BlindMemberIDs = %#v, want nil for null", response.BlindMemberIDs)
	}
}

func TestDecodeChatInfoResponseDistinguishesAbsentAndEmptyBlindMembers(t *testing.T) {
	absent, err := DecodeChatInfoResponse(mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if absent.BlindMemberIDs != nil {
		t.Fatalf("absent bmids = %#v, want nil", absent.BlindMemberIDs)
	}
	empty, err := DecodeChatInfoResponse(mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}}},
		{Key: "bmids", Value: bson.A{}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if empty.BlindMemberIDs == nil || len(empty.BlindMemberIDs) != 0 {
		t.Fatalf("empty bmids = %#v, want non-nil empty slice", empty.BlindMemberIDs)
	}
}

func TestDecodeChatInfoResponseDerivesDisplayMembers(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{
			{Key: "c", Value: int64(42)},
			{Key: "t", Value: "MultiChat"},
			{Key: "displayMembers", Value: bson.A{
				bson.D{{Key: "userId", Value: int64(7)}, {Key: "nickName", Value: "Seven"}, {Key: "suspicion", Value: ""}},
				bson.D{{Key: "userId", Value: int64(8)}, {Key: "nickName", Value: "Eight"}},
			}},
		}},
	})
	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	got := response.ChatData
	if !reflect.DeepEqual(got.DisplayUserIDs, []int64{7, 8}) {
		t.Fatalf("DisplayUserIDs = %v", got.DisplayUserIDs)
	}
	if !reflect.DeepEqual(got.DisplayNicknames, []string{"Seven", "Eight"}) {
		t.Fatalf("DisplayNicknames = %v", got.DisplayNicknames)
	}
	if !reflect.DeepEqual(got.Suspicions, []string{"", ""}) {
		t.Fatalf("Suspicions = %v", got.Suspicions)
	}
	if got.DisplayImageURLs != nil {
		t.Fatalf("DisplayImageURLs = %v, want nil with more than one display member", got.DisplayImageURLs)
	}
}

func TestDecodeChatInfoResponseSingleDisplayMemberImageKeyDependsOnRawType(t *testing.T) {
	displayMember := bson.D{
		{Key: "userId", Value: int64(7)},
		{Key: "nickName", Value: "Seven"},
		{Key: "pi", Value: "https://example.invalid/open.jpg"},
		{Key: "profileImageUrl", Value: "https://example.invalid/normal.jpg"},
	}
	for _, test := range []struct {
		name      string
		typeField bson.E
		want      string
	}{
		{name: "open direct uses pi", typeField: bson.E{Key: "type", Value: "OD"}, want: "https://example.invalid/open.jpg"},
		{name: "open group uses pi", typeField: bson.E{Key: "type", Value: "OM"}, want: "https://example.invalid/open.jpg"},
		{name: "direct uses profileImageUrl", typeField: bson.E{Key: "type", Value: "DirectChat"}, want: "https://example.invalid/normal.jpg"},
		// The official initializer consults the raw "type" key, not the
		// mapped "t" key, when choosing the image key.
		{name: "short t key is not consulted", typeField: bson.E{Key: "t", Value: "OM"}, want: "https://example.invalid/normal.jpg"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mustMarshal(t, bson.D{
				{Key: "chatInfo", Value: bson.D{
					{Key: "c", Value: int64(42)},
					test.typeField,
					{Key: "displayMembers", Value: bson.A{displayMember}},
				}},
			})
			response, err := DecodeChatInfoResponse(body)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(response.ChatData.DisplayImageURLs, []string{test.want}) {
				t.Fatalf("DisplayImageURLs = %v, want [%s]", response.ChatData.DisplayImageURLs, test.want)
			}
		})
	}
}

func TestDecodeChatInfoResponseMappedDisplayArraysOverrideDisplayMembers(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{
			{Key: "c", Value: int64(42)},
			{Key: "displayMembers", Value: bson.A{
				bson.D{{Key: "userId", Value: int64(7)}, {Key: "nickName", Value: "Seven"}},
			}},
			{Key: "i", Value: bson.A{int64(9)}},
			{Key: "k", Value: bson.A{"Nine"}},
		}},
	})
	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response.ChatData.DisplayUserIDs, []int64{9}) || !reflect.DeepEqual(response.ChatData.DisplayNicknames, []string{"Nine"}) {
		t.Fatalf("display arrays = %v %v, want mapped i/k values", response.ChatData.DisplayUserIDs, response.ChatData.DisplayNicknames)
	}
}

func TestDecodeChatInfoResponseMetaFromJSONStringWithStringBooleans(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "chatInfo", Value: bson.D{
			{Key: "c", Value: int64(42)},
			{Key: "m", Value: `{"name":"Synthetic Room","imageUrl":"https://example.invalid/r.jpg","favorite":"true","chat_hide":"false"}`},
		}},
	})
	response, err := DecodeChatInfoResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	want := &RoomMeta{Name: "Synthetic Room", ImageURL: "https://example.invalid/r.jpg", Favorite: true, ChatHide: false}
	if !reflect.DeepEqual(response.ChatData.Meta, want) {
		t.Fatalf("Meta = %#v, want %#v", response.ChatData.Meta, want)
	}
}

func TestDecodeChatInfoResponseFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		body bson.D
	}{
		{name: "missing chatInfo", body: bson.D{{Key: "status", Value: int32(0)}}},
		{name: "chatInfo not a document", body: bson.D{{Key: "chatInfo", Value: "x"}}},
		{name: "missing chat id", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "t", Value: "DirectChat"}}}}},
		{name: "non-positive chat id", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(0)}}}}},
		{name: "chat id wrong type", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: "42"}}}}},
		{name: "int32 field overflow", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "a", Value: int64(1) << 40}}}}},
		{name: "display ids wrong element", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "i", Value: bson.A{"7"}}}}}},
		{name: "display member not a document", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "displayMembers", Value: bson.A{int64(7)}}}}}},
		{name: "last chat log wrong type", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "l", Value: int64(1)}}}}},
		{name: "meta malformed JSON", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "m", Value: "{"}}}}},
		{name: "bmids wrong type", body: bson.D{{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}}}, {Key: "bmids", Value: "99"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeChatInfoResponse(mustMarshal(t, test.body)); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
	if _, err := DecodeChatInfoResponse([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("malformed BSON error = %v, want ErrInvalidResponse", err)
	}
}

func TestMemberRequestEncodesChatIDAndInt64MemberIDs(t *testing.T) {
	body, err := MemberRequest{ChatID: 42, MemberIDs: []int64{7, 8}}.MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	elements, err := raw.Elements()
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 2 || elements[0].Key() != "chatId" || elements[1].Key() != "memberIds" {
		t.Fatalf("elements = %v, want chatId then memberIds", elements)
	}
	if value := elements[0].Value(); value.Type != bson.TypeInt64 || value.Int64() != 42 {
		t.Fatalf("chatId = %v", value)
	}
	values, err := elements[1].Value().Array().Values()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Type != bson.TypeInt64 || values[0].Int64() != 7 || values[1].Int64() != 8 {
		t.Fatalf("memberIds = %v, want int64 [7 8]", values)
	}
}

func TestMemberRequestValidation(t *testing.T) {
	tooMany := make([]int64, MaxMemberBatch+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for _, test := range []struct {
		name    string
		request MemberRequest
	}{
		{name: "chat id", request: MemberRequest{ChatID: 0, MemberIDs: []int64{7}}},
		{name: "empty", request: MemberRequest{ChatID: 42}},
		{name: "non-positive member", request: MemberRequest{ChatID: 42, MemberIDs: []int64{7, 0}}},
		{name: "over batch limit", request: MemberRequest{ChatID: 42, MemberIDs: tooMany}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.request.MarshalBSON(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestDecodeMemberResponseReadsProfileKeys(t *testing.T) {
	body := mustMarshal(t, bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "chatId", Value: int64(42)},
		{Key: "members", Value: bson.A{
			bson.D{
				{Key: "userId", Value: int64(7)},
				{Key: "nickName", Value: "Seven"},
				{Key: "pi", Value: "https://example.invalid/7.jpg"},
				{Key: "fpi", Value: "https://example.invalid/7-full.jpg"},
				{Key: "opi", Value: "https://example.invalid/7-original.jpg"},
				{Key: "type", Value: int32(2)},
				{Key: "ut", Value: int32(1)},
				{Key: "accountId", Value: int32(70)},
				{Key: "linkedServices", Value: ""},
				{Key: "statusMessage", Value: "synthetic status"},
				{Key: "countryIso", Value: "US"},
				{Key: "suspended", Value: false},
				{Key: "memorial", Value: false},
				{Key: "accessPermit", Value: "a"},
				{Key: "suspicion", Value: ""},
				{Key: "pli", Value: int64(0)},
				{Key: "mt", Value: int32(0)},
				{Key: "pfId", Value: int64(0)},
			},
			bson.D{
				{Key: "userId", Value: int32(8)},
				{Key: "nickName", Value: "Eight"},
			},
		}},
	})
	response, err := DecodeMemberResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	want := MemberResponse{
		ChatID: 42,
		Members: []Member{
			{
				UserID:                  7,
				Nickname:                "Seven",
				ProfileImageURL:         "https://example.invalid/7.jpg",
				FullProfileImageURL:     "https://example.invalid/7-full.jpg",
				OriginalProfileImageURL: "https://example.invalid/7-original.jpg",
				Type:                    2,
				UserType:                1,
				AccountID:               70,
				StatusMessage:           "synthetic status",
				CountryISO:              "US",
				AccessPermit:            "a",
			},
			{UserID: 8, Nickname: "Eight"},
		},
	}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response =\n%#v\nwant\n%#v", response, want)
	}
}

func TestDecodeMemberResponseEmptyAndNullMembers(t *testing.T) {
	for _, members := range []any{bson.A{}, nil} {
		response, err := DecodeMemberResponse(mustMarshal(t, bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "members", Value: members},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Members) != 0 {
			t.Fatalf("members = %v, want empty", response.Members)
		}
	}
}

func TestDecodeMemberResponseFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		body bson.D
	}{
		{name: "missing chat id", body: bson.D{{Key: "members", Value: bson.A{}}}},
		{name: "non-positive chat id", body: bson.D{{Key: "chatId", Value: int64(0)}, {Key: "members", Value: bson.A{}}}},
		{name: "members wrong type", body: bson.D{{Key: "members", Value: "x"}}},
		{name: "member not a document", body: bson.D{{Key: "members", Value: bson.A{int64(7)}}}},
		{name: "member missing user id", body: bson.D{{Key: "members", Value: bson.A{bson.D{{Key: "nickName", Value: "x"}}}}}},
		{name: "member non-positive user id", body: bson.D{{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(0)}}}}}},
		{name: "nickname wrong type", body: bson.D{{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(7)}, {Key: "nickName", Value: int32(1)}}}}}},
		{name: "chat id wrong type", body: bson.D{{Key: "chatId", Value: "42"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeMemberResponse(mustMarshal(t, test.body)); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestMemberListRequestEncodesChatIDAndToken(t *testing.T) {
	body, err := MemberListRequest{ChatID: 42, Token: 0}.MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	elements, err := bson.Raw(body).Elements()
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 2 || elements[0].Key() != "chatId" || elements[1].Key() != "token" {
		t.Fatalf("elements = %v, want chatId then token", elements)
	}
	if value := elements[1].Value(); value.Type != bson.TypeInt64 || value.Int64() != 0 {
		t.Fatalf("token = %v, want int64(0)", value)
	}
	if _, err := (MemberListRequest{ChatID: 0}).MarshalBSON(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("chat id 0 error = %v, want ErrInvalidRequest", err)
	}
}

func TestDecodeMemberListResponse(t *testing.T) {
	response, err := DecodeMemberListResponse(mustMarshal(t, bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "token", Value: int64(5)},
		{Key: "type", Value: "synthetic"},
		{Key: "memberIds", Value: bson.A{int64(7), int32(8)}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := MemberListResponse{Token: 5, Type: "synthetic", MemberIDs: []int64{7, 8}}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response = %#v, want %#v", response, want)
	}
	if _, err := DecodeMemberListResponse(mustMarshal(t, bson.D{{Key: "memberIds", Value: bson.A{"7"}}})); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("wrong element error = %v, want ErrInvalidResponse", err)
	}
}
