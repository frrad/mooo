package connector

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

func TestKakaoMessageMetadataRoundTripsThroughJSON(t *testing.T) {
	original := newKakaoMessageMetadata(3000, 11, 2000, messagetype.Text, strings.Repeat("x", 120), 0)
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded KakaoMessageMetadata
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != *original || len([]rune(decoded.Preview)) != 100 {
		t.Fatalf("decoded metadata = %+v", decoded)
	}
	message := &database.Message{ID: makeMessageID(3000, 11), Room: makePortalKey(3000, "1000"), SenderID: makeUserID(2000), Metadata: &decoded}
	if _, err := metadataFromMessage(message); err != nil {
		t.Fatalf("decoded metadata rejected: %v", err)
	}
}

func TestReplyPreviewDoesNotSplitSurrogatePair(t *testing.T) {
	preview := truncateReplyPreview(strings.Repeat("a", 99) + "😀")
	if preview != strings.Repeat("a", 99) {
		t.Fatalf("preview = %q", preview)
	}
}

func TestKakaoMessageMetadataPersistsThroughBridgeDatabase(t *testing.T) {
	ctx := context.Background()
	br, _ := newFrameworkBridge(t, frameworkOpts{})
	db := br.DB
	converted, err := convertPhoto(ctx, nil, nil, events.PhotoMessage{Message: media.PhotoMessage{
		ChatID: 3000, LogID: 11, AuthorID: 2000,
		Attachment: media.PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want, ok := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
	if !ok || want.ConversionGap != "expired" {
		t.Fatalf("converted metadata = %#v", converted.Parts[0].DBMetadata)
	}
	insertAndCheck := func(message *database.Message, expected *KakaoMessageMetadata) {
		t.Helper()
		if err := db.Message.Insert(ctx, message); err != nil {
			t.Fatal(err)
		}
		got, err := db.Message.GetPartByID(ctx, networkid.UserLoginID("1000"), message.ID, message.PartID)
		if err != nil {
			t.Fatal(err)
		}
		metadata, ok := got.Metadata.(*KakaoMessageMetadata)
		if !ok || *metadata != *expected {
			t.Fatalf("round-tripped metadata = %#v, want %#v", got.Metadata, expected)
		}
	}
	// Ordinary source metadata must survive the same database path as gap metadata.
	sourceMetadata := newKakaoMessageMetadata(3000, 10, 2000, messagetype.Text, "persisted", 0)
	insertAndCheck(&database.Message{
		BridgeID: "test", ID: makeMessageID(3000, 10), PartID: "0", MXID: "$source-event",
		Room: makePortalKey(3000, "1000"), SenderID: makeUserID(2000), SenderMXID: "@sender:test",
		Timestamp: time.Unix(1700000000, 0), Metadata: sourceMetadata,
	}, sourceMetadata)
	// Deterministic conversion notices retain the original source identity and gap category.
	insertAndCheck(&database.Message{
		BridgeID: "test", ID: makeMessageID(3000, 11), PartID: "0", MXID: "$gap-event",
		Room: makePortalKey(3000, "1000"), SenderID: makeUserID(2000), SenderMXID: "@sender:test",
		Timestamp: time.Unix(1700000000, 0), Metadata: want,
	}, want)
}

func TestReactionRevisionSurvivesSQLiteCloseAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bridge.db")
	br, raw := newFrameworkBridge(t, frameworkOpts{path: path})
	db := br.DB
	want := newKakaoMessageMetadata(3000, 11, 2000, messagetype.Text, "persisted", 0)
	want.ReactionRevision = 12
	want.MiniReactionRevision = 3
	message := &database.Message{
		BridgeID: "test", ID: makeMessageID(3000, 11), PartID: "0", MXID: "$event",
		Room: makePortalKey(3000, "1000"), SenderID: makeUserID(2000), SenderMXID: "@sender:test",
		Timestamp: time.Unix(1700000000, 0), Metadata: want,
	}
	if err := db.Message.Insert(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := raw.RawDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := newFrameworkBridge(t, frameworkOpts{path: path})
	got, err := reopened.DB.Message.GetPartByID(ctx, networkid.UserLoginID("1000"), message.ID, message.PartID)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := got.Metadata.(*KakaoMessageMetadata)
	if !ok || metadata.ReactionRevision != 12 || metadata.MiniReactionRevision != 3 {
		t.Fatalf("reopened reaction revision = %#v", got.Metadata)
	}
}
