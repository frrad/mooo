package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/loco"
)

type contactPhotoClient interface {
	ContactProfilePhoto(context.Context, int64, int64, *http.Client) ([]byte, error)
	Shutdown(context.Context) error
}

func contactPhoto(args []string, stdout io.Writer, packetObserver func(loco.Packet)) error {
	return runContactPhoto(args, stdout, func(path string) (contactPhotoClient, error) {
		return client.OpenWithOptions(path, &http.Client{Timeout: 20 * time.Second}, client.OpenOptions{PacketObserver: packetObserver})
	})
}

func runContactPhoto(args []string, stdout io.Writer, open func(string) (contactPhotoClient, error)) error {
	options, err := parseOptions(args, "state", "output", "chat", "user")
	chatID, chatErr := strconv.ParseInt(options["chat"], 10, 64)
	userID, userErr := strconv.ParseInt(options["user"], 10, 64)
	if err != nil || chatErr != nil || userErr != nil || chatID <= 0 || userID <= 0 || !filepath.IsAbs(options["state"]) || !filepath.IsAbs(options["output"]) {
		return errors.New("contacts photo requires absolute --state and --output and positive --chat and --user")
	}
	file, commit, abort, err := reservePrivateOutput(options["output"], "photo")
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
	data, err := c.ContactProfilePhoto(ctx, chatID, userID, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		if errors.Is(err, client.ErrContactPhotoAbsent) {
			return errors.New("contact has no profile photo")
		}
		return errors.New("contact photo retrieval failed")
	}
	closed = true
	if shutdownProfile(c) != nil {
		return errors.New("selected profile cleanup failed")
	}
	if _, err = file.Write(data); err != nil || commit() != nil {
		return errors.New("could not write photo output")
	}
	_, _ = fmt.Fprintf(stdout, "saved %d photo bytes in private output\n", len(data))
	return nil
}
