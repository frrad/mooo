package connector

import (
	"encoding/json"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2/database"

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
