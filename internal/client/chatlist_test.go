package client

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestListChatsDecodesFullInventoryIncludingEmptyRooms(t *testing.T) {
	s := fullChatListSession(t, []bson.Raw{
		mustBSON(bson.D{{Key: "c", Value: int64(43)}, {Key: "t", Value: "MultiChat"}, {Key: "a", Value: int32(3)}, {Key: "m", Value: bson.D{{Key: "name", Value: "Synthetic room"}}}}),
		mustBSON(bson.D{{Key: "c", Value: int64(42)}, {Key: "t", Value: "DirectChat"}, {Key: "n", Value: int32(2)}}),
	})
	c := &Client{session: s}
	chats, err := c.ListChats(context.Background())
	if err != nil || len(chats) != 2 {
		t.Fatalf("list: %d, %v", len(chats), err)
	}
	if chats[0].ChatID != 42 || chats[0].NewMessageCount != 2 || chats[1].ChatID != 43 || chats[1].Meta == nil || chats[1].Meta.Name != "Synthetic room" {
		t.Fatalf("unexpected typed inventory: %#v", chats)
	}
	if len(chats[0].LastChatLog) != 0 {
		t.Fatal("empty room invented a last message")
	}
}

func TestListChatsRejectsPartialOrDeltaInventory(t *testing.T) {
	for _, cursor := range []loginCursor{{}, {complete: true}, {replaceInventory: true}} {
		s := &Session{loginCursor: cursor}
		if _, err := s.ListChats(); !errors.Is(err, ErrChatListIncomplete) {
			t.Fatalf("list error: %v", err)
		}
	}
	if _, err := (*Client)(nil).ListChats(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Fatalf("nil client: %v", err)
	}
}

func TestListChatsFailsOnMalformedRoomInsteadOfDroppingIt(t *testing.T) {
	s := fullChatListSession(t, []bson.Raw{mustBSON(bson.D{{Key: "c", Value: int64(42)}}), mustBSON(bson.D{{Key: "c", Value: int64(43)}, {Key: "t", Value: int32(123)}})})
	if chats, err := s.ListChats(); err == nil || chats != nil {
		t.Fatalf("malformed room returned successful partial inventory: %v", err)
	}
}

func TestListCompletionOnlyFollowsSuccessfulEOF(t *testing.T) {
	page := mustBSON(bson.D{{Key: "status", Value: int32(0)}, {Key: "eof", Value: true}, {Key: "chatDatas", Value: bson.A{}}})
	for _, status := range []int32{0, -305, -310} {
		_, cursor, err := finishLoginSyncSession(context.Background(), nil, page, status, nil)
		if err != nil || cursor.complete != (status == 0) {
			t.Fatalf("status %d completion=%v error=%v", status, cursor.complete, err)
		}
	}
}

func TestLaterListPageDeletionRemovesEarlierRecoveryTarget(t *testing.T) {
	var cursor loginCursor
	first := mustBSON(bson.D{
		{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}}}},
	})
	second := mustBSON(bson.D{{Key: "delChatIds", Value: bson.A{int64(42)}}})
	if err := updateLoginCursor(first, &cursor, false); err != nil {
		t.Fatal(err)
	}
	if err := updateLoginCursor(second, &cursor, true); err != nil {
		t.Fatal(err)
	}
	if len(cursor.observed) != 0 {
		t.Fatal("later deletion retained the earlier page target")
	}
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 50); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.InstallSession(nil, nil, cursor.observed, cursor.deleted, false); err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Snapshot().KnownChats) != 0 || len(checkpoint.Snapshot().Chats) != 0 {
		t.Fatal("deleted room survived session installation")
	}
}

func fullChatListSession(t *testing.T, rooms []bson.Raw) *Session {
	t.Helper()
	values := make(bson.A, 0, len(rooms))
	for _, room := range rooms {
		values = append(values, room)
	}
	page := mustBSON(bson.D{{Key: "status", Value: int32(0)}, {Key: "eof", Value: true}, {Key: "chatDatas", Value: values}})
	raw, cursor, err := finishLoginSyncSession(t.Context(), nil, page, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	cursor.replaceInventory = true
	return &Session{loginCursor: cursor, initialChatData: raw}
}

func TestListChatsAppliesPageDeletionBeforeRecreationAndLatestMetadata(t *testing.T) {
	var cursor loginCursor
	pages := []bson.D{
		{{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}, {Key: "t", Value: "DirectChat"}}, bson.D{{Key: "c", Value: int64(43)}, {Key: "t", Value: "DirectChat"}}}}},
		{{Key: "delChatIds", Value: bson.A{int64(42), int64(43)}}, {Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}, {Key: "t", Value: "MultiChat"}, {Key: "a", Value: int32(3)}}}}},
		{{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}, {Key: "t", Value: "MultiChat"}, {Key: "a", Value: int32(4)}}}}},
	}
	for _, page := range pages {
		if err := updateLoginCursor(mustBSON(page), &cursor, false); err != nil {
			t.Fatal(err)
		}
	}
	cursor.complete, cursor.replaceInventory = true, true
	s := &Session{loginCursor: cursor}
	chats, err := s.ListChats()
	if err != nil || len(chats) != 1 || chats[0].ChatID != 42 || chats[0].ActiveMemberCount != 4 {
		t.Fatalf("recreated latest inventory: %#v, %v", chats, err)
	}
}
