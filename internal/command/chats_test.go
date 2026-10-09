package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

type fakeChatLister struct {
	chats  []chatmeta.ChatData
	err    error
	closed int
}

func (f *fakeChatLister) ListChats(context.Context) ([]chatmeta.ChatData, error) {
	return f.chats, f.err
}
func (f *fakeChatLister) Close() error { f.closed++; return nil }

func TestChatListWritesPrivateSummaryWithoutSourceMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "chats.json")
	fake := &fakeChatLister{chats: []chatmeta.ChatData{{ChatID: 42, Type: "DirectChat", Meta: &chatmeta.RoomMeta{Name: "Synthetic room"}, LastChatLog: []byte("SOURCE-PRIVATE")}}}
	var stdout, stderr bytes.Buffer
	code := runListChats([]string{"--state", filepath.Join(root, "profile"), "--output", out}, &stdout, &stderr, func(string) (chatLister, error) { return fake, nil })
	if code != 0 || fake.closed != 1 {
		t.Fatalf("code=%d closes=%d error=%s", code, fake.closed, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 || !bytes.Contains(data, []byte("Synthetic room")) || bytes.Contains(data, []byte("SOURCE-PRIVATE")) {
		t.Fatal("unsafe or incomplete private summary")
	}
	if strings.Contains(stdout.String(), "Synthetic room") || strings.Contains(stdout.String(), root) {
		t.Fatal("private values printed to console")
	}
}

func TestChatListIncompleteRemovesReservedOutput(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	out := filepath.Join(root, "chats.json")
	fake := &fakeChatLister{err: client.ErrChatListIncomplete}
	var stdout, stderr bytes.Buffer
	if runListChats([]string{"--state", filepath.Join(root, "profile"), "--output", out}, &stdout, &stderr, func(string) (chatLister, error) { return fake, nil }) == 0 {
		t.Fatal("incomplete listing succeeded")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed listing left an output file")
	}
	if fake.closed != 1 {
		t.Fatal("selected client not closed")
	}
}

func TestChatListExistingOutputDoesNotOpenProfile(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	out := filepath.Join(root, "chats.json")
	_ = os.WriteFile(out, []byte("keep"), 0600)
	opened := false
	var stdout, stderr bytes.Buffer
	code := runListChats([]string{"--state", filepath.Join(root, "profile"), "--output", out}, &stdout, &stderr, func(string) (chatLister, error) { opened = true; return nil, errors.New("unused") })
	if code == 0 || opened {
		t.Fatal("existing output reached profile open")
	}
	data, _ := os.ReadFile(out)
	if string(data) != "keep" {
		t.Fatal("existing output overwritten")
	}
}
