package client

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func testMetadataSession(backend *scriptedBackend, userID int64) *Session {
	session := &Session{
		wire:    backend.client,
		nextID:  1,
		userID:  userID,
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	go session.readLoop()
	return session
}

func requireMemberIDs(raw bson.Raw, want []int64) error {
	value, err := raw.LookupErr("memberIds")
	if err != nil || value.Type != bson.TypeArray {
		return errors.New("memberIds is not an array")
	}
	values, err := value.Array().Values()
	if err != nil {
		return err
	}
	if len(values) != len(want) {
		return fmt.Errorf("memberIds length = %d, want %d", len(values), len(want))
	}
	for i, element := range values {
		if element.Type != bson.TypeInt64 || element.Int64() != want[i] {
			return fmt.Errorf("memberIds[%d] = %v, want int64(%d)", i, element, want[i])
		}
	}
	return nil
}

func TestSessionChatInfoSendsOnceAndDecodes(t *testing.T) {
	backend := newScriptedBackend(t, false, expectRequest("CHATINFO", func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatId"); err != nil {
			return err
		}
		return requireInt64(raw, "chatId", 42)
	}, statusDocument(
		bson.E{Key: "chatInfo", Value: bson.D{
			{Key: "c", Value: int64(42)},
			{Key: "t", Value: "DirectChat"},
			{Key: "a", Value: int32(2)},
			{Key: "i", Value: bson.A{int64(7)}},
			{Key: "k", Value: bson.A{"Seven"}},
		}},
	)))
	session := testMetadataSession(backend, 1)

	response, err := session.ChatInfo(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if response.ChatData.ChatID != 42 || response.ChatData.Type != "DirectChat" ||
		!reflect.DeepEqual(response.ChatData.DisplayNicknames, []string{"Seven"}) {
		t.Fatalf("ChatData = %#v", response.ChatData)
	}
	backend.wait(t)
}

func TestSessionChatInfoNonzeroStatusIsFailure(t *testing.T) {
	// The official completion treats only status zero as success; partial
	// success (-310) is not accepted for CHATINFO.
	backend := newScriptedBackend(t, false, expectRequest("CHATINFO", nil, bson.D{
		{Key: "status", Value: int32(-310)},
	}))
	session := testMetadataSession(backend, 1)
	var status StatusError
	if _, err := session.ChatInfo(context.Background(), 42); !errors.As(err, &status) || status.Status != -310 {
		t.Fatalf("ChatInfo error = %v, want StatusError(-310)", err)
	}
	backend.wait(t)
}

func TestSessionChatInfoRejectsInvalidChatIDBeforeTransport(t *testing.T) {
	session := &Session{}
	if _, err := session.ChatInfo(context.Background(), 0); !errors.Is(err, chatmeta.ErrInvalidRequest) {
		t.Fatalf("ChatInfo error = %v, want ErrInvalidRequest", err)
	}
}

func TestSessionMembersFiltersSelfAndNonPositiveIDs(t *testing.T) {
	backend := newScriptedBackend(t, false, expectRequest("MEMBER", func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatId", "memberIds"); err != nil {
			return err
		}
		if err := requireInt64(raw, "chatId", 42); err != nil {
			return err
		}
		// User 1 is the logged-in account; 0 and -5 are not valid IDs.
		return requireMemberIDs(raw, []int64{7, 8})
	}, statusDocument(
		bson.E{Key: "chatId", Value: int64(42)},
		bson.E{Key: "members", Value: bson.A{
			bson.D{{Key: "userId", Value: int64(7)}, {Key: "nickName", Value: "Seven"}},
			bson.D{{Key: "userId", Value: int64(8)}, {Key: "nickName", Value: "Eight"}},
		}},
	)))
	session := testMetadataSession(backend, 1)

	members, err := session.Members(context.Background(), 42, []int64{7, 1, 0, 8, -5})
	if err != nil {
		t.Fatal(err)
	}
	want := []chatmeta.Member{{UserID: 7, Nickname: "Seven"}, {UserID: 8, Nickname: "Eight"}}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("members = %#v, want %#v", members, want)
	}
	backend.wait(t)
}

func TestSessionMembersSendsNothingWhenOnlySelfRemains(t *testing.T) {
	// No wire: any request attempt would fail with ErrClosed.
	session := &Session{userID: 1, pending: make(map[uint32]chan requestResult)}
	members, err := session.Members(context.Background(), 42, []int64{1, 0})
	if err != nil {
		t.Fatalf("Members error = %v, want nil without a request", err)
	}
	if members != nil {
		t.Fatalf("members = %#v, want nil", members)
	}
}

func TestSessionMembersBatchesAtFiveHundred(t *testing.T) {
	ids := make([]int64, 0, 501)
	for id := int64(2); id <= 502; id++ {
		ids = append(ids, id)
	}
	firstBatch := ids[:500]
	secondBatch := ids[500:]
	backend := newScriptedBackend(t, false,
		expectRequest("MEMBER", func(raw bson.Raw) error {
			return requireMemberIDs(raw, firstBatch)
		}, statusDocument(
			bson.E{Key: "chatId", Value: int64(42)},
			bson.E{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(2)}}}},
		)),
		expectRequest("MEMBER", func(raw bson.Raw) error {
			return requireMemberIDs(raw, secondBatch)
		}, statusDocument(
			bson.E{Key: "chatId", Value: int64(42)},
			bson.E{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(502)}}}},
		)),
	)
	session := testMetadataSession(backend, 1)

	members, err := session.Members(context.Background(), 42, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].UserID != 2 || members[1].UserID != 502 {
		t.Fatalf("members = %#v, want users 2 and 502 from two batches", members)
	}
	backend.wait(t)
}

func TestSessionMembersStopsAtFirstFailedBatch(t *testing.T) {
	ids := make([]int64, 0, 501)
	for id := int64(2); id <= 502; id++ {
		ids = append(ids, id)
	}
	requests := 0
	backend := newScriptedBackend(t, false, expectRequest("MEMBER", func(bson.Raw) error {
		requests++
		return nil
	}, bson.D{{Key: "status", Value: int32(-500)}}))
	session := testMetadataSession(backend, 1)

	var status StatusError
	if _, err := session.Members(context.Background(), 42, ids); !errors.As(err, &status) {
		t.Fatalf("Members error = %v, want StatusError", err)
	}
	backend.wait(t)
	if requests != 1 {
		t.Fatalf("requests = %d, want 1: later batches must be abandoned", requests)
	}
}

func TestSessionMembersReturnsCompletedBatchesWithLaterFailure(t *testing.T) {
	ids := make([]int64, 0, 501)
	for id := int64(2); id <= 502; id++ {
		ids = append(ids, id)
	}
	backend := newScriptedBackend(t, false,
		expectRequest("MEMBER", nil, statusDocument(
			bson.E{Key: "chatId", Value: int64(42)},
			bson.E{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(2)}}}},
		)),
		expectRequest("MEMBER", nil, bson.D{{Key: "status", Value: int32(-500)}}),
	)
	session := testMetadataSession(backend, 1)

	members, err := session.Members(context.Background(), 42, ids)
	if err == nil {
		t.Fatal("Members unexpectedly succeeded")
	}
	if len(members) != 1 || members[0].UserID != 2 {
		t.Fatalf("members = %#v, want the completed first batch", members)
	}
	backend.wait(t)
}

func TestSessionMemberListSendsChatIDAndToken(t *testing.T) {
	backend := newScriptedBackend(t, false, expectRequest("MEMLIST", func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatId", "token"); err != nil {
			return err
		}
		if err := requireInt64(raw, "chatId", 42); err != nil {
			return err
		}
		return requireInt64(raw, "token", 3)
	}, statusDocument(
		bson.E{Key: "token", Value: int64(4)},
		bson.E{Key: "memberIds", Value: bson.A{int64(7), int64(8)}},
	)))
	session := testMetadataSession(backend, 1)

	response, err := session.MemberList(context.Background(), 42, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := chatmeta.MemberListResponse{Token: 4, MemberIDs: []int64{7, 8}}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response = %#v, want %#v", response, want)
	}
	backend.wait(t)
}
