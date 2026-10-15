package client

import (
	"errors"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

// checkResumePairs requires a LOGINLIST that resumes chats 3 and 9 at log IDs
// 30 and 90 with lbk 6 and the given lastTokenId.
func checkResumePairs(lastTokenID int64) func(bson.Raw) error {
	return func(raw bson.Raw) error {
		if err := requireInt64(raw, "lastTokenId", lastTokenID); err != nil {
			return err
		}
		chatIDs, err := raw.LookupErr("chatIds")
		if err != nil || chatIDs.Type != bson.TypeArray {
			return errors.New("chatIds is not an array")
		}
		maxIDs, err := raw.LookupErr("maxIds")
		if err != nil || maxIDs.Type != bson.TypeArray {
			return errors.New("maxIds is not an array")
		}
		chatValues, _ := chatIDs.Array().Values()
		maxValues, _ := maxIDs.Array().Values()
		if len(chatValues) != 2 || len(maxValues) != 2 || chatValues[0].Int64() != 3 || maxValues[0].Int64() != 30 || chatValues[1].Int64() != 9 || maxValues[1].Int64() != 90 {
			return fmt.Errorf("resume pairs = %v / %v", chatValues, maxValues)
		}
		lbk, err := raw.LookupErr("lbk")
		if err != nil || lbk.Type != bson.TypeInt32 || lbk.Int32() != 6 {
			return errors.New("lbk is not int32(6)")
		}
		return nil
	}
}

func TestScriptedBackendResumedLoginUsesDurableCursors(t *testing.T) {
	dialers, backends := scriptedLogin(t, testloco.Expect("LOGINLIST", checkResumePairs(77), statusDocument(
		bson.E{Key: "chatDatas", Value: bson.A{}},
		bson.E{Key: "eof", Value: true},
		bson.E{Key: "lastTokenId", Value: int64(91)},
		bson.E{Key: "lbk", Value: int32(8)},
	)), testloco.HoldOpen())
	resume := continuity.Checkpoint{
		Version: continuity.Version, LastTokenID: 77, LBK: 6,
		Chats: []continuity.ChatCursor{{ChatID: 3, MaxLogID: 30}, {ChatID: 9, MaxLogID: 90}},
	}
	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{resume: resume, dialers: dialers, ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if session.loginCursor.lastTokenID == nil || *session.loginCursor.lastTokenID != 91 || session.loginCursor.lbk == nil || *session.loginCursor.lbk != 8 {
		t.Fatalf("login cursor = %#v", session.loginCursor)
	}
	_ = session.Close()
	waitBackends(t, backends...)
}

func TestScriptedBackendFullListPreservesDurableMessageCursors(t *testing.T) {
	dialers, backends := scriptedLogin(t, testloco.Expect("LOGINLIST", checkResumePairs(0), statusDocument(
		bson.E{Key: "chatDatas", Value: bson.A{}},
		bson.E{Key: "eof", Value: true},
		bson.E{Key: "lastTokenId", Value: int64(91)},
		bson.E{Key: "lbk", Value: int32(8)},
	)), testloco.HoldOpen())
	resume := continuity.Checkpoint{
		Version: continuity.Version, LastTokenID: 77, LBK: 6,
		Chats: []continuity.ChatCursor{{ChatID: 3, MaxLogID: 30}, {ChatID: 9, MaxLogID: 90}},
	}
	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{resume: (OpenOptions{FullChatList: true}).loginResume(resume), dialers: dialers, ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if session.loginCursor.lastTokenID == nil || *session.loginCursor.lastTokenID != 91 || session.loginCursor.lbk == nil || *session.loginCursor.lbk != 8 {
		t.Fatalf("login cursor = %#v", session.loginCursor)
	}
	if resume.LastTokenID != 77 {
		t.Fatal("full-list mode mutated the durable checkpoint")
	}
	chats, err := session.ListChats()
	if err != nil || len(chats) != 0 {
		t.Fatalf("completed empty full list: %v, %v", chats, err)
	}
	_ = session.Close()
	waitBackends(t, backends...)
}

func TestScriptedBackendPartialLChatListKeepsDeltasWithoutAdvancingGlobalCursor(t *testing.T) {
	dialers, backends := scriptedLogin(t,
		testloco.Expect("LOGINLIST", nil, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}}}},
			bson.E{Key: "eof", Value: false},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lastChatId", Value: int64(42)},
			bson.E{Key: "lbk", Value: int32(3)},
		)),
		testloco.Expect("LCHATLIST", func(raw bson.Raw) error {
			if err := requireInt64(raw, "lastTokenId", 11); err != nil {
				return err
			}
			return requireInt64(raw, "lastChatId", 42)
		}, bson.D{
			{Key: "status", Value: int32(-310)},
			{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(43)}}}},
			{Key: "delChatIds", Value: bson.A{int64(42)}},
			{Key: "eof", Value: false},
		}),
		testloco.HoldOpen(),
	)
	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers, ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if len(session.initialChatData) != 2 {
		t.Fatalf("chat deltas = %d, want 2", len(session.initialChatData))
	}
	if len(session.loginCursor.observed) != 1 || session.loginCursor.observed[0].ChatID != 43 || len(session.loginCursor.deleted) != 1 || session.loginCursor.deleted[0] != 42 {
		t.Fatalf("inventory delta = %#v", session.loginCursor)
	}
	if session.loginCursor.lastTokenID != nil || session.loginCursor.lbk != nil {
		t.Fatalf("partial page advanced global cursor: %#v", session.loginCursor)
	}
	if session.loginCursor.replaceInventory {
		t.Fatal("partial full-list request must not replace the known inventory")
	}

	_ = session.Close()
	waitBackends(t, backends...)
}

func TestScriptedBackendFullPagedListRemovesEarlierRoom(t *testing.T) {
	dialers, backends := scriptedLogin(t,
		testloco.Expect("LOGINLIST", nil, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}}}},
			bson.E{Key: "eof", Value: false},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lastChatId", Value: int64(42)},
			bson.E{Key: "lbk", Value: int32(3)},
		)),
		testloco.Expect("LCHATLIST", func(raw bson.Raw) error {
			if err := requireInt64(raw, "lastTokenId", 11); err != nil {
				return err
			}
			return requireInt64(raw, "lastChatId", 42)
		}, bson.D{
			{Key: "status", Value: int32(0)},
			{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(43)}}}},
			{Key: "delChatIds", Value: bson.A{int64(42)}},
			{Key: "eof", Value: true},
		}),
		testloco.HoldOpen(),
	)
	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers, ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if len(session.initialChatData) != 2 {
		t.Fatalf("chat deltas = %d, want 2", len(session.initialChatData))
	}
	if len(session.loginCursor.observed) != 1 || session.loginCursor.observed[0].ChatID != 43 || len(session.loginCursor.deleted) != 1 || session.loginCursor.deleted[0] != 42 {
		t.Fatalf("inventory delta = %#v", session.loginCursor)
	}
	chats, err := session.ListChats()
	if err != nil || len(chats) != 1 || chats[0].ChatID != 43 {
		t.Fatalf("completed paged inventory: %#v, %v", chats, err)
	}

	_ = session.Close()
	waitBackends(t, backends...)
}

func TestScriptedBackendLoginAlreadyCurrentDoesNotPageOrAdvanceGlobalCursor(t *testing.T) {
	dialers, backends := scriptedLoginAttempt(t, "token", bson.D{
		{Key: "status", Value: int32(-305)},
		{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}}}},
		{Key: "delChatIds", Value: bson.A{int64(9)}},
		{Key: "eof", Value: false},
		{Key: "lastTokenId", Value: int64(91)},
		{Key: "lastChatId", Value: int64(42)},
		{Key: "lbk", Value: int32(8)},
	})
	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers, ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if len(session.initialChatData) != 1 || len(session.loginCursor.observed) != 1 || len(session.loginCursor.deleted) != 1 {
		t.Fatalf("already-current delta = %#v, chats=%d", session.loginCursor, len(session.initialChatData))
	}
	if session.loginCursor.lastTokenID != nil || session.loginCursor.lbk != nil {
		t.Fatalf("already-current response advanced global cursor: %#v", session.loginCursor)
	}
	// A delta-only login does not complete a recovery episode.
	if session.loginStatus != sessionlogin.StatusLoginListDeltaOnly || (&Client{session: session}).LoginCompleted() {
		t.Fatalf("delta-only login status = %d, completed = %v", session.loginStatus, (&Client{session: session}).LoginCompleted())
	}
	_ = session.Close()
	waitBackends(t, backends...)
}
