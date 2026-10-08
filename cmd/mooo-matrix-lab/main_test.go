package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestRawEncryptedEventParsedForSDK(t *testing.T) {
	var evt event.Event
	data := []byte(`{"type":"m.room.encrypted","room_id":"!synthetic:localhost","content":{"algorithm":"m.megolm.v1.aes-sha2","ciphertext":"synthetic","session_id":"synthetic"}}`)
	if err := parseEncryptedEvent(data, &evt); err != nil {
		t.Fatal(err)
	}
	if _, ok := evt.Content.Parsed.(*event.EncryptedEventContent); !ok {
		t.Fatal("SDK decrypt requires parsed encrypted content")
	}
}

func TestAttemptNeverReplayed(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "attempt")
	if err := reserveAttempt(path); err != nil {
		t.Fatal(err)
	}
	if err := writeReceipt(path, "$synthetic"); err != nil {
		t.Fatal(err)
	}
	if err := reserveAttempt(path); err == nil {
		t.Fatal("duplicate attempt accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "$synthetic\n" {
		t.Fatal("existing receipt changed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("receipt not private")
	}
}

func TestCredentialsReuseWithoutLogin(t *testing.T) {
	var logins int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_matrix/client/v3/account/whoami" && r.Header.Get("Authorization") == "Bearer synthetic" {
			_, _ = w.Write([]byte(`{"user_id":"@synthetic:localhost","device_id":"SYNTHETIC"}`))
		} else {
			logins++
			w.WriteHeader(401)
		}
	}))
	defer server.Close()
	path := filepath.Join(privateTempDir(t), "credentials")
	data, _ := json.Marshal(deviceCredentials{id.UserID("@synthetic:localhost"), id.DeviceID("SYNTHETIC"), "synthetic"})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cli, err := mautrix.NewClient(server.URL, "@synthetic:localhost", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticate(context.Background(), cli, options{user: "@synthetic:localhost", device: "SYNTHETIC", credentials: path}); err != nil {
		t.Fatal(err)
	}
	if logins != 0 {
		t.Fatal("credentials reuse attempted login")
	}
	if err := authenticate(context.Background(), cli, options{user: "@different:localhost", device: "SYNTHETIC", credentials: path}); err == nil {
		t.Fatal("identity mismatch accepted")
	}
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPrivateFilesFailClosed(t *testing.T) {
	dir := privateTempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("synthetic"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(target); err == nil {
		t.Fatal("broad permissions accepted")
	}
	if err := os.Chmod(target, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := reserveAttempt(filepath.Join(dir, "attempt")); err == nil {
		t.Fatal("public directory accepted")
	}
}

func TestExpectedNativeStickerEvent(t *testing.T) {
	evt := &event.Event{Type: event.EventSticker, Content: event.Content{Parsed: &event.MessageEventContent{Body: "KakaoTalk sticker"}}}
	expected := expectedMessage{EventType: "m.sticker", Body: "KakaoTalk sticker"}
	if !matchesExpected(evt, expected) {
		t.Fatal("native sticker rejected")
	}
	evt.Type = event.EventMessage
	if matchesExpected(evt, expected) {
		t.Fatal("ordinary message accepted as sticker")
	}
	expected = expectedMessage{Type: "m.text", Body: "KakaoTalk sticker"}
	evt.Content.Parsed.(*event.MessageEventContent).MsgType = event.MsgText
	if !matchesExpected(evt, expected) {
		t.Fatal("existing text expectation broken")
	}
}
