package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/frrad/mooo/internal/client"
)

type contactPhotoClient interface {
	ContactProfilePhoto(context.Context, int64, int64, *http.Client) ([]byte, error)
	Close() error
}

func openContactPhotoClient(path string) (contactPhotoClient, error) {
	return client.Open(path, &http.Client{Timeout: 20 * time.Second})
}

func runContactPhoto(args []string, stdout, stderr io.Writer, open func(string) (contactPhotoClient, error)) int {
	options, err := parseOptions(args, map[string]struct{}{"state": {}, "output": {}, "chat": {}, "user": {}})
	chatID, chatErr := strconv.ParseInt(options["chat"], 10, 64)
	userID, userErr := strconv.ParseInt(options["user"], 10, 64)
	if err != nil || chatErr != nil || userErr != nil || chatID <= 0 || userID <= 0 || !filepath.IsAbs(options["state"]) || !filepath.IsAbs(options["output"]) {
		return commandError(stderr, "contacts photo requires absolute --state and --output and positive --chat and --user")
	}
	parent, err := os.Lstat(filepath.Dir(options["output"]))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return commandError(stderr, "photo output requires a private directory")
	}
	file, err := os.OpenFile(options["output"], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return commandError(stderr, "could not reserve photo output")
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
	data, err := c.ContactProfilePhoto(ctx, chatID, userID, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		if errors.Is(err, client.ErrContactPhotoAbsent) {
			return commandError(stderr, "contact has no profile photo")
		}
		return commandError(stderr, "contact photo retrieval failed")
	}
	closed = true
	if c.Close() != nil {
		return commandError(stderr, "selected profile cleanup failed")
	}
	if _, err = file.Write(data); err != nil {
		return commandError(stderr, "could not write photo output")
	}
	if file.Sync() != nil || file.Close() != nil {
		return commandError(stderr, "could not write photo output")
	}
	keep = true
	_, _ = fmt.Fprintf(stdout, "saved %d photo bytes in private output\n", len(data))
	return 0
}
