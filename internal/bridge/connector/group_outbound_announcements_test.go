package connector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestMatrixTopicChangeIsRejectedOnceAndRestoresKakaoAnnouncement(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	bot := &announcementIntent{}
	br, err := newFrameworkConversionBridge(ctx, raw, bot)
	if err != nil {
		t.Fatal(err)
	}
	portal, err := br.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	portal.Topic = "Synthetic Kakao announcement"
	portal.TopicSet = true
	backend := &fakeKakao{}
	kc := newKakaoClient(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{}}, Bridge: br, Log: zerolog.Nop()}, testSelfID, nil)
	kc.client = backend

	topics, ok := any(kc).(bridgev2.RoomTopicHandlingNetworkAPI)
	if !ok {
		t.Fatal("Matrix topic changes have no explicit handling path")
	}
	for _, requested := range []string{"Synthetic Matrix topic", ""} {
		bot.topics = nil
		changed, err := topics.HandleMatrixRoomTopic(ctx, &bridgev2.MatrixRoomTopic{
			MatrixEventBase: bridgev2.MatrixEventBase[*event.TopicEventContent]{
				Event:   &event.Event{Type: event.StateTopic},
				Content: &event.TopicEventContent{Topic: requested},
				Portal:  portal,
			},
		})
		if changed {
			t.Fatalf("topic %q reported a portal change", requested)
		}
		var status bridgev2.MessageStatus
		if !errors.As(err, &status) {
			t.Fatalf("topic %q error is not a message status: %v", requested, err)
		}
		if status.Status != event.MessageStatusFail || status.ErrorReason != event.MessageStatusUnsupported || !status.IsCertain || !status.SendNotice {
			t.Fatalf("topic %q status = %+v", requested, status)
		}
		if portal.Topic != "Synthetic Kakao announcement" {
			t.Fatalf("topic %q changed the portal topic to %q", requested, portal.Topic)
		}
		if len(bot.topics) != 1 || bot.topics[0] != "Synthetic Kakao announcement" {
			t.Fatalf("topic %q restored Matrix topics %q", requested, bot.topics)
		}
	}
	if len(backend.calls) != 0 || len(backend.sends) != 0 || len(backend.replies) != 0 || backend.imageCalls != 0 {
		t.Fatalf("rejected topic changes reached KakaoTalk: calls=%q", backend.calls)
	}
}
