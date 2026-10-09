package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

type chatLister interface {
	ListChats(context.Context) ([]chatmeta.ChatData, error)
	Close() error
}

type chatSummary struct {
	ChatID       int64    `json:"chat_id"`
	Type         string   `json:"type"`
	Name         string   `json:"name,omitempty"`
	DisplayNames []string `json:"display_names,omitempty"`
	Members      int32    `json:"active_member_count"`
	Unread       int32    `json:"unread_count"`
}

func openChatLister(path string) (chatLister, error) {
	return client.OpenWithOptions(path, &http.Client{Timeout: 20 * time.Second}, client.OpenOptions{FullChatList: true})
}

func runListChats(args []string, stdout, stderr io.Writer, open func(string) (chatLister, error)) int {
	options, err := parseOptions(args, map[string]struct{}{"state": {}, "output": {}})
	if err != nil || !filepath.IsAbs(options["state"]) || !filepath.IsAbs(options["output"]) {
		return commandError(stderr, "chats list requires absolute --state and --output")
	}
	// Validate the destination before authenticating. Results contain private
	// room identifiers and names, never raw messages, profiles or credentials.
	parent, err := os.Lstat(filepath.Dir(options["output"]))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return commandError(stderr, "chat output requires a private directory")
	}
	file, err := os.OpenFile(options["output"], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return commandError(stderr, "could not reserve chat output")
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(options["output"])
		}
	}()
	c, err := open(options["state"])
	if err != nil {
		return commandError(stderr, "could not open selected profile")
	}
	closed := false
	defer func() {
		if !closed {
			_ = c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	chats, err := c.ListChats(ctx)
	if err != nil {
		return commandError(stderr, "complete chat listing failed")
	}
	closed = true
	if c.Close() != nil {
		return commandError(stderr, "selected profile cleanup failed")
	}
	result := make([]chatSummary, 0, len(chats))
	for _, chat := range chats {
		entry := chatSummary{ChatID: chat.ChatID, Type: chat.Type, DisplayNames: chat.DisplayNicknames, Members: chat.ActiveMemberCount, Unread: chat.NewMessageCount}
		if chat.Meta != nil {
			entry.Name = chat.Meta.Name
		}
		result = append(result, entry)
	}
	if json.NewEncoder(file).Encode(result) != nil || file.Sync() != nil || file.Close() != nil {
		return commandError(stderr, "could not write chat output")
	}
	keep = true
	_, _ = fmt.Fprintf(stdout, "listed %d chats in private output\n", len(result))
	return 0
}
