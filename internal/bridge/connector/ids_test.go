package connector

import (
	"errors"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestPortalKeyUsesDecimalChatIDAndLoginReceiver(t *testing.T) {
	key := makePortalKey(9007199254740993, networkid.UserLoginID("42"))
	if key.ID != networkid.PortalID("9007199254740993") {
		t.Fatalf("portal ID = %q", key.ID)
	}
	if key.Receiver != networkid.UserLoginID("42") {
		t.Fatalf("receiver = %q", key.Receiver)
	}
	chatID, err := parseChatID(key.ID)
	if err != nil || chatID != 9007199254740993 {
		t.Fatalf("parseChatID = %d, %v", chatID, err)
	}
}

func TestMessageIDIsScopedToChat(t *testing.T) {
	id := makeMessageID(100, 200)
	if id != networkid.MessageID("100:200") {
		t.Fatalf("message ID = %q", id)
	}
	chatID, logID, err := parseMessageID(id)
	if err != nil || chatID != 100 || logID != 200 {
		t.Fatalf("parseMessageID = %d, %d, %v", chatID, logID, err)
	}
}

func TestParseRejectsNonCanonicalIDs(t *testing.T) {
	for _, value := range []string{"", "0", "-5", "+5", "05", " 5", "5 ", "abc", "9223372036854775808"} {
		if _, err := parseID(value); !errors.Is(err, errInvalidID) {
			t.Errorf("parseID(%q) error = %v, want errInvalidID", value, err)
		}
	}
	for _, value := range []string{"", "100", "100:", ":200", "100:200:300", "100:0", "x:200"} {
		if _, _, err := parseMessageID(networkid.MessageID(value)); !errors.Is(err, errInvalidID) {
			t.Errorf("parseMessageID(%q) error = %v, want errInvalidID", value, err)
		}
	}
}

func TestProfileStatePathStaysInsideProfileDir(t *testing.T) {
	path, err := profileStatePath("/srv/profiles", "lab-1")
	if err != nil || path != filepath.Join("/srv/profiles", "lab-1") {
		t.Fatalf("profileStatePath = %q, %v", path, err)
	}
	for _, name := range []string{"", ".", "..", "../etc/passwd", "a/b", `a\b`, ".hidden", "-flag", "name with space"} {
		if _, err := profileStatePath("/srv/profiles", name); !errors.Is(err, errInvalidProfileName) {
			t.Errorf("profileStatePath(%q) error = %v, want errInvalidProfileName", name, err)
		}
	}
	if _, err := profileStatePath("", "lab-1"); err == nil {
		t.Fatal("profileStatePath accepted an unconfigured directory")
	}
}
