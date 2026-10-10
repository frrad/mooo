package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/loco"
)

type chatLister interface {
	ListChats(context.Context) ([]chatmeta.ChatData, error)
	Shutdown(context.Context) error
}

type chatSummary struct {
	ChatID       int64    `json:"chat_id"`
	Type         string   `json:"type"`
	Name         string   `json:"name,omitempty"`
	DisplayNames []string `json:"display_names,omitempty"`
	Members      int32    `json:"active_member_count"`
	Unread       int32    `json:"unread_count"`
}

func listChats(args []string, stdout io.Writer, packetObserver func(loco.Packet)) error {
	return runListChats(args, stdout, func(path string) (chatLister, error) {
		return client.OpenWithOptions(path, &http.Client{Timeout: 20 * time.Second}, client.OpenOptions{FullChatList: true, PacketObserver: packetObserver})
	})
}

func runListChats(args []string, stdout io.Writer, open func(string) (chatLister, error)) error {
	options, err := parseOptions(args, "state", "output")
	if err != nil || !filepath.IsAbs(options["state"]) || !filepath.IsAbs(options["output"]) {
		return errors.New("chats list requires absolute --state and --output")
	}
	// Validate the destination before authenticating. Results contain private
	// room identifiers and names, never raw messages, profiles or credentials.
	file, commit, abort, err := reservePrivateOutput(options["output"], "chat")
	if err != nil {
		return err
	}
	defer abort()
	c, err := open(options["state"])
	if err != nil {
		return errors.New("could not open selected profile")
	}
	closed := false
	defer func() {
		if !closed {
			_ = shutdownProfile(c)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	chats, err := c.ListChats(ctx)
	if err != nil {
		return errors.New("complete chat listing failed")
	}
	closed = true
	if shutdownProfile(c) != nil {
		return errors.New("selected profile cleanup failed")
	}
	result := make([]chatSummary, 0, len(chats))
	for _, chat := range chats {
		entry := chatSummary{ChatID: chat.ChatID, Type: chat.Type, DisplayNames: chat.DisplayNicknames, Members: chat.ActiveMemberCount, Unread: chat.NewMessageCount}
		if chat.Meta != nil {
			entry.Name = chat.Meta.Name
		}
		result = append(result, entry)
	}
	if json.NewEncoder(file).Encode(result) != nil || commit() != nil {
		return errors.New("could not write chat output")
	}
	_, _ = fmt.Fprintf(stdout, "listed %d chats in private output\n", len(result))
	return nil
}
