package connector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/frrad/mooo/internal/protocol/chat"
)

func TestKakaoMessageMetadataRoundTripsThroughJSON(t *testing.T) {
	original := newKakaoMessageMetadata(3000, 11, 2000, chat.TextType, strings.Repeat("x", 120), 0)
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
	raw, err := dbutil.NewWithDialect(":memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	db := database.New(networkid.BridgeID("test"), (&KakaoConnector{}).GetDBMetaTypes(), raw)
	if err := db.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	// Satisfy the message foreign keys with minimal synthetic parent rows.
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','2000','sender','','','','',0,0,0,'[]',NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test','3000','1000',NULL,NULL,'','','','', '', '', '', '', '',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	want := newKakaoMessageMetadata(3000, 11, 2000, chat.TextType, "persisted", 0)
	message := &database.Message{
		BridgeID: "test", ID: makeMessageID(3000, 11), PartID: "0", MXID: "$event",
		Room: makePortalKey(3000, "1000"), SenderID: makeUserID(2000), SenderMXID: "@sender:test",
		Timestamp: time.Unix(1700000000, 0), Metadata: want,
	}
	if err := db.Message.Insert(ctx, message); err != nil {
		t.Fatal(err)
	}
	got, err := db.Message.GetPartByID(ctx, networkid.UserLoginID("1000"), message.ID, message.PartID)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := got.Metadata.(*KakaoMessageMetadata)
	if !ok || *metadata != *want {
		t.Fatalf("round-tripped metadata = %#v, want %#v", got.Metadata, want)
	}
}
