package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const defaultDeviceID = "MOOO_MATRIX_LAB"

type failFastSyncer struct {
	*mautrix.DefaultSyncer
	processed func()
}

func (s *failFastSyncer) ProcessResponse(ctx context.Context, resp *mautrix.RespSync, since string) error {
	err := s.DefaultSyncer.ProcessResponse(ctx, resp, since)
	if err == nil && s.processed != nil {
		s.processed()
	}
	return err
}

func (s *failFastSyncer) OnFailedSync(_ *mautrix.RespSync, err error) (time.Duration, error) {
	if err == nil {
		return 0, errors.New("sync failed")
	}
	return 0, errors.New("sync failed")
}

type options struct {
	op, homeserver, user, passwordFile, pickleFile, cryptoDB, device, room, input, expected, receipt, credentials string
	connect, timeout                                                                                              time.Duration
}

func main() {
	op := flag.String("op", "validate", "validate, startup, sync, decrypt, send-text, or send-file")
	connect := flag.Bool("connect", false, "allow startup to use Matrix; off by default")
	homeserver := flag.String("homeserver", "", "owned localhost Matrix homeserver URL")
	user := flag.String("user-id", "", "Matrix user ID")
	passwordFile := flag.String("password-file", "", "0600 password file")
	pickleFile := flag.String("pickle-key-file", "", "0600 pickle-key file")
	cryptoDB := flag.String("crypto-db", "", "private SQLite crypto/state database")
	device := flag.String("device-id", defaultDeviceID, "stable private device ID")
	room := flag.String("room-id", "", "existing owned portal room ID")
	input := flag.String("input", "", "private body, file, or encrypted event JSON")
	expected := flag.String("expected", "", "private expected decrypted fixture JSON")
	receipt := flag.String("receipt-file", "", "private 0600 file for send event ID")
	timeout := flag.Duration("timeout", 30*time.Second, "bounded operation timeout")
	credentials := flag.String("credentials-file", "", "private persistent device credentials; login only when absent")
	flag.Parse()

	o := options{op: *op, homeserver: *homeserver, user: *user, passwordFile: *passwordFile, pickleFile: *pickleFile, cryptoDB: *cryptoDB, device: *device, room: *room, input: *input, expected: *expected, receipt: *receipt, credentials: *credentials, timeout: *timeout}
	if *connect {
		o.connect = 1
	}
	if err := validate(o); err != nil {
		fail("invalid", err)
	}
	if o.op == "validate" {
		fmt.Println("validated")
		return
	}
	if kind := run(o); kind != "ok" {
		fail(kind, nil)
	}
	fmt.Println(*op + "_ok")
}

func run(o options) (kind string) {
	pickle, err := readPrivate(o.pickleFile)
	if err != nil {
		return "pickle_key_file"
	}
	cli, err := mautrix.NewClient(o.homeserver, id.UserID(o.user), "")
	if err != nil {
		return "client"
	}
	cli.Log = zerolog.Nop()
	cli.Syncer = &failFastSyncer{DefaultSyncer: mautrix.NewDefaultSyncer()}
	// The companion performs exactly one operation per invocation. Disable the
	// SDK's gateway/network retry loop; the outer timeout remains bounded.
	cli.DefaultHTTPRetries = 0
	cli.ResponseSizeLimit = 4 << 20
	helper, err := cryptohelper.NewCryptoHelper(cli, []byte(pickle), o.cryptoDB)
	if err != nil {
		return "crypto_helper"
	}
	defer func() {
		if closeErr := helper.Close(); closeErr != nil && kind == "ok" {
			kind = "crypto_close"
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	if err = authenticate(ctx, cli, o); err != nil {
		switch err.Error() {
		case "credentials mismatch", "device mismatch", "credentials unavailable", "login identity mismatch":
			return strings.ReplaceAll(err.Error(), " ", "_")
		}
		if errors.Is(err, mautrix.MLimitExceeded) {
			var resp mautrix.RespError
			if errors.As(err, &resp) {
				if ms, ok := resp.ExtraData["retry_after_ms"].(float64); ok {
					fmt.Fprintf(os.Stderr, "retry_after_ms:%.0f\n", ms)
				}
			}
			return "credentials_rate_limited"
		}
		if errors.Is(err, mautrix.MForbidden) {
			return "credentials_forbidden"
		}
		return "credentials"
	}
	if err = helper.Init(ctx); err != nil {
		return "crypto_init"
	}
	switch o.op {
	case "startup":
		return "ok"
	case "sync":
		var observed atomic.Bool
		syncer := cli.Syncer.(*failFastSyncer)
		syncCtx, cancelSync := context.WithCancel(ctx)
		defer cancelSync()
		var processed atomic.Bool
		syncer.processed = func() {
			processed.Store(true)
			cancelSync()
		}
		cli.Syncer.(mautrix.ExtensibleSyncer).OnSync(func(context.Context, *mautrix.RespSync, string) bool { observed.Store(true); return true })
		err = cli.SyncWithContext(syncCtx)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return "sync"
		}
		if !observed.Load() || !processed.Load() {
			return "sync_unobserved"
		}
		if err := preflightRoom(ctx, cli, id.RoomID(o.room)); err != nil {
			return "room_state"
		}
		return "ok"
	case "decrypt":
		data, readErr := os.ReadFile(o.input)
		if readErr != nil {
			return "event_file"
		}
		var evt event.Event
		if parseEncryptedEvent(data, &evt) != nil {
			return "event_json"
		}
		if evt.Type != event.EventEncrypted {
			return "decrypt_mismatch"
		}
		decrypted, decErr := helper.Decrypt(ctx, &evt)
		if decErr != nil {
			return "decrypt"
		}
		expectedData, readErr := os.ReadFile(o.expected)
		if readErr != nil {
			return "expected_file"
		}
		var expected expectedMessage
		if json.Unmarshal(expectedData, &expected) != nil || expected.Body == "" || (expected.EventType != "m.sticker" && expected.Type == "") {
			return "expected_json"
		}
		if decrypted.RoomID != id.RoomID(o.room) || !matchesExpected(decrypted, expected) {
			return "decrypt_mismatch"
		}
		content := decrypted.Content.Parsed.(*event.MessageEventContent)

		if expected.SHA256 != "" && content.URL != "" {
			return "media_plaintext_url"
		}
		if expected.SHA256 != "" && content.File != nil {
			uri, parseErr := content.File.URL.Parse()
			if parseErr != nil {
				return "media_uri"
			}
			resp, downloadErr := cli.Download(ctx, uri)
			if downloadErr != nil || resp == nil || resp.Body == nil {
				return "media_download"
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
			_ = resp.Body.Close()
			if readErr != nil || len(data) > 4<<20 {
				return "media_download"
			}
			if content.File.DecryptInPlace(data) != nil {
				return "media_decrypt"
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != strings.ToLower(expected.SHA256) {
				return "media_mismatch"
			}
		} else if expected.SHA256 != "" {
			return "media_missing"
		}
		return "ok"
	case "send-text":
		if reserveAttempt(o.receipt) != nil {
			return "attempt_receipt"
		}
		if err := preflightRoom(ctx, cli, id.RoomID(o.room)); err != nil {
			return "room_state"
		}
		body, readErr := os.ReadFile(o.input)
		if readErr != nil || len(bytes.TrimSpace(body)) == 0 || len(body) > 4<<10 {
			return "body_file"
		}
		content := &event.MessageEventContent{MsgType: event.MsgText, Body: string(body)}
		encrypted, encErr := helper.Encrypt(ctx, id.RoomID(o.room), event.EventMessage, content)
		if encErr != nil {
			return "encrypt"
		}
		resp, sendErr := cli.SendMessageEvent(ctx, id.RoomID(o.room), event.EventEncrypted, encrypted)
		if sendErr != nil {
			return "send"
		}
		if o.receipt != "" && (resp == nil || writeReceipt(o.receipt, string(resp.EventID)) != nil) {
			return "receipt"
		}
		return "ok"
	case "send-file":
		if reserveAttempt(o.receipt) != nil {
			return "attempt_receipt"
		}
		if err := preflightRoom(ctx, cli, id.RoomID(o.room)); err != nil {
			return "room_state"
		}
		plain, readErr := os.ReadFile(o.input)
		if readErr != nil || len(plain) == 0 || len(plain) > 4<<20 {
			return "file"
		}
		config, format, decodeErr := image.DecodeConfig(bytes.NewReader(plain))
		if decodeErr != nil || format != "png" || config.Width != 64 || config.Height != 64 {
			return "file_shape"
		}
		file := attachment.NewEncryptedFile()
		file.EncryptInPlace(plain)
		upload, uploadErr := cli.UploadMedia(ctx, mautrix.ReqUploadMedia{ContentBytes: plain, ContentType: "application/octet-stream", FileName: filepath.Base(o.input)})
		if uploadErr != nil {
			return "upload"
		}
		content := &event.MessageEventContent{MsgType: event.MsgImage, Body: filepath.Base(o.input), Info: &event.FileInfo{MimeType: "image/png", Width: 64, Height: 64, Size: len(plain)}, File: &event.EncryptedFileInfo{EncryptedFile: *file, URL: id.ContentURIString(upload.ContentURI.String())}}
		encrypted, encErr := helper.Encrypt(ctx, id.RoomID(o.room), event.EventMessage, content)
		if encErr != nil {
			return "encrypt"
		}
		resp, sendErr := cli.SendMessageEvent(ctx, id.RoomID(o.room), event.EventEncrypted, encrypted)
		if sendErr != nil {
			return "send"
		}
		if resp == nil || writeReceipt(o.receipt, string(resp.EventID)) != nil {
			return "receipt"
		}
		return "ok"
	default:
		return "invalid"
	}
}

func validate(o options) error {
	if !map[string]bool{"validate": true, "startup": true, "sync": true, "decrypt": true, "send-text": true, "send-file": true}[o.op] {
		return errors.New("unsupported operation")
	}
	if o.op != "validate" && o.connect == 0 {
		return errors.New("operation requires --connect")
	}
	u, err := url.ParseRequestURI(o.homeserver)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() != "localhost" {
		return errors.New("homeserver must be owned localhost HTTP(S)")
	}
	if !strings.HasPrefix(o.user, "@") || !strings.Contains(o.user, ":") {
		return errors.New("invalid user ID")
	}
	for _, path := range []string{o.passwordFile, o.pickleFile, o.cryptoDB, o.credentials} {
		if path == "" || !filepath.IsAbs(path) {
			return errors.New("private paths must be absolute")
		}
	}
	if o.op != "validate" && (o.room == "" && o.op != "startup" && o.op != "sync" && o.op != "decrypt") {
		return errors.New("room required")
	}
	if (o.op == "sync" || o.op == "send-text" || o.op == "send-file") && o.room == "" {
		return errors.New("room required")
	}
	if (o.op == "sync" || o.op == "decrypt") && o.room == "" {
		return errors.New("room required")
	}
	if o.op == "decrypt" && (o.input == "" || o.expected == "") {
		return errors.New("event input required")
	}
	if o.op == "send-text" && (o.room == "" || o.input == "") {
		return errors.New("room and body input required")
	}
	if o.op == "send-file" && (o.room == "" || o.input == "") {
		return errors.New("room and file input required")
	}
	if o.device == "" || strings.ContainsAny(o.device, " \t\r\n") {
		return errors.New("invalid device ID")
	}
	if o.timeout <= 0 || o.timeout > 5*time.Minute {
		return errors.New("timeout out of bounds")
	}
	if (o.op == "send-text" || o.op == "send-file") && o.receipt == "" {
		return errors.New("send requires attempt receipt")
	}
	if o.receipt != "" && (!filepath.IsAbs(o.receipt) || strings.Contains(o.receipt, "\n")) {
		return errors.New("receipt path invalid")
	}
	return nil
}

func writeReceipt(path, value string) error {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return errors.New("receipt empty")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.WriteString(value + "\n"); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Chmod(0o600)
}

func readPrivate(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", errors.New("private file unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("private file permissions too broad")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("private file unreadable")
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("private file empty")
	}
	return value, nil
}

func fail(kind string, _ error) {
	fmt.Fprintln(os.Stderr, "failure:"+kind)
	os.Exit(1)
}

func preflightRoom(ctx context.Context, cli *mautrix.Client, room id.RoomID) error {
	state, err := cli.State(ctx, room)
	if err != nil {
		return err
	}
	for _, events := range state {
		for _, evt := range events {
			cli.StateStoreSyncHandler(ctx, evt)
		}
	}
	enc := state[event.StateEncryption][""]
	if enc == nil || enc.Content.AsEncryption().Algorithm != id.AlgorithmMegolmV1 {
		return errors.New("room encryption algorithm unavailable")
	}
	members, err := cli.JoinedMembers(ctx, room)
	if err != nil || members == nil || len(members.Joined) < 2 {
		return errors.New("room membership incomplete")
	}
	if _, ok := members.Joined[cli.UserID]; !ok {
		return errors.New("tester is not joined")
	}
	return nil
}

// A receipt is reserved before any upload or send. An incomplete attempt must
// be inspected by the operator, never replayed automatically.
func reserveAttempt(path string) error {
	if err := privateParent(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString("attempt_started\n")
	if err != nil {
		return err
	}
	return f.Sync()
}

type deviceCredentials struct {
	User   id.UserID   `json:"user_id"`
	Device id.DeviceID `json:"device_id"`
	Token  string      `json:"access_token"`
}

func authenticate(ctx context.Context, cli *mautrix.Client, o options) error {
	if err := privateParent(o.credentials); err != nil {
		return err
	}
	if value, err := readPrivate(o.credentials); err == nil {
		var creds deviceCredentials
		if json.Unmarshal([]byte(value), &creds) != nil || creds.User != id.UserID(o.user) || creds.Device != id.DeviceID(o.device) || creds.Token == "" {
			return errors.New("credentials mismatch")
		}
		cli.AccessToken, cli.DeviceID = creds.Token, creds.Device
		who, err := cli.Whoami(ctx)
		if err != nil {
			return err
		}
		if who.UserID != creds.User || who.DeviceID != creds.Device {
			return errors.New("device mismatch")
		}
		return nil
	} else if _, statErr := os.Lstat(o.credentials); !os.IsNotExist(statErr) {
		return errors.New("credentials unavailable")
	}
	password, err := readPrivate(o.passwordFile)
	if err != nil {
		return err
	}
	resp, err := cli.Login(ctx, &mautrix.ReqLogin{Type: mautrix.AuthTypePassword, Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: o.user}, Password: password, DeviceID: id.DeviceID(o.device), StoreCredentials: true, InitialDeviceDisplayName: "mooo matrix lab"})
	if err != nil {
		return err
	}
	if resp.UserID != id.UserID(o.user) || resp.DeviceID != id.DeviceID(o.device) || resp.AccessToken == "" {
		return errors.New("login identity mismatch")
	}
	data, err := json.Marshal(deviceCredentials{resp.UserID, resp.DeviceID, resp.AccessToken})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(o.credentials, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(data)
	if err != nil {
		return err
	}
	return f.Sync()
}

func parseEncryptedEvent(data []byte, evt *event.Event) error {
	if err := json.Unmarshal(data, evt); err != nil {
		return err
	}
	if evt.Type != event.EventEncrypted {
		return errors.New("expected encrypted event")
	}
	return evt.Content.ParseRaw(evt.Type)
}

func privateParent(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("private path must be absolute")
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("private directory required")
	}
	return nil
}

type expectedMessage struct {
	EventType string `json:"event_type"`
	Type      string `json:"type"`
	Body      string `json:"body"`
	SHA256    string `json:"sha256"`
}

func matchesExpected(evt *event.Event, expected expectedMessage) bool {
	typ := event.EventMessage
	if expected.EventType == "m.sticker" {
		typ = event.EventSticker
	} else if expected.EventType != "" && expected.EventType != "m.room.message" {
		return false
	}
	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	return evt.Type == typ && ok && string(content.MsgType) == expected.Type && content.Body == expected.Body
}
