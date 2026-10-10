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
	if err := run(o); err != nil {
		fail(exitKind(err), err)
	}
	fmt.Println(*op + "_ok")
}

// Each failure the companion reports maps to exactly one sentinel. The
// sentinel's text is printed as "failure:<kind>" and is part of the CLI
// contract consumed by private lab scripts.
var (
	errInvalid                = errors.New("invalid")
	errPickleKeyFile          = errors.New("pickle_key_file")
	errClient                 = errors.New("client")
	errCryptoHelper           = errors.New("crypto_helper")
	errCryptoClose            = errors.New("crypto_close")
	errCryptoInit             = errors.New("crypto_init")
	errCredentials            = errors.New("credentials")
	errCredentialsMismatch    = errors.New("credentials_mismatch")
	errDeviceMismatch         = errors.New("device_mismatch")
	errCredentialsUnavailable = errors.New("credentials_unavailable")
	errLoginIdentityMismatch  = errors.New("login_identity_mismatch")
	errCredentialsRateLimited = errors.New("credentials_rate_limited")
	errCredentialsForbidden   = errors.New("credentials_forbidden")
	errSync                   = errors.New("sync")
	errSyncUnobserved         = errors.New("sync_unobserved")
	errRoomState              = errors.New("room_state")
	errEventFile              = errors.New("event_file")
	errEventJSON              = errors.New("event_json")
	errDecryptMismatch        = errors.New("decrypt_mismatch")
	errDecrypt                = errors.New("decrypt")
	errExpectedFile           = errors.New("expected_file")
	errExpectedJSON           = errors.New("expected_json")
	errMediaPlaintextURL      = errors.New("media_plaintext_url")
	errMediaURI               = errors.New("media_uri")
	errMediaDownload          = errors.New("media_download")
	errMediaDecrypt           = errors.New("media_decrypt")
	errMediaMismatch          = errors.New("media_mismatch")
	errMediaMissing           = errors.New("media_missing")
	errAttemptReceipt         = errors.New("attempt_receipt")
	errBodyFile               = errors.New("body_file")
	errEncrypt                = errors.New("encrypt")
	errSend                   = errors.New("send")
	errReceipt                = errors.New("receipt")
	errFile                   = errors.New("file")
	errFileShape              = errors.New("file_shape")
	errUpload                 = errors.New("upload")
)

var exitKinds = []error{
	errInvalid, errPickleKeyFile, errClient, errCryptoHelper, errCryptoClose, errCryptoInit,
	errCredentials, errCredentialsMismatch, errDeviceMismatch, errCredentialsUnavailable,
	errLoginIdentityMismatch, errCredentialsRateLimited, errCredentialsForbidden,
	errSync, errSyncUnobserved, errRoomState,
	errEventFile, errEventJSON, errDecryptMismatch, errDecrypt, errExpectedFile, errExpectedJSON,
	errMediaPlaintextURL, errMediaURI, errMediaDownload, errMediaDecrypt, errMediaMismatch, errMediaMissing,
	errAttemptReceipt, errBodyFile, errEncrypt, errSend, errReceipt, errFile, errFileShape, errUpload,
}

// exitKind maps an operation error to its reported failure kind. An error
// that wraps no sentinel is reported as "invalid".
func exitKind(err error) string {
	for _, sentinel := range exitKinds {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	return errInvalid.Error()
}

// labClient is the authenticated SDK state shared by every connected operation.
type labClient struct {
	cli    *mautrix.Client
	helper *cryptohelper.CryptoHelper
	room   id.RoomID
}

func run(o options) (err error) {
	pickle, err := readPrivate(o.pickleFile)
	if err != nil {
		return errPickleKeyFile
	}
	cli, err := mautrix.NewClient(o.homeserver, id.UserID(o.user), "")
	if err != nil {
		return errClient
	}
	cli.Log = zerolog.Nop()
	cli.Syncer = &failFastSyncer{DefaultSyncer: mautrix.NewDefaultSyncer()}
	// The companion performs exactly one operation per invocation. Disable the
	// SDK's gateway/network retry loop; the outer timeout remains bounded.
	cli.DefaultHTTPRetries = 0
	cli.ResponseSizeLimit = 4 << 20
	helper, err := cryptohelper.NewCryptoHelper(cli, []byte(pickle), o.cryptoDB)
	if err != nil {
		return errCryptoHelper
	}
	defer func() {
		if closeErr := helper.Close(); closeErr != nil && err == nil {
			err = errCryptoClose
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	if err = authenticate(ctx, cli, o); err != nil {
		return authenticationFailure(err)
	}
	if err = helper.Init(ctx); err != nil {
		return errCryptoInit
	}
	lab := labClient{cli: cli, helper: helper, room: id.RoomID(o.room)}
	switch o.op {
	case "startup":
		return startup()
	case "sync":
		return lab.syncRoom(ctx)
	case "decrypt":
		return lab.decrypt(ctx, o)
	case "send-text":
		return lab.sendText(ctx, o)
	case "send-file":
		return lab.sendFile(ctx, o)
	default:
		return errInvalid
	}
}

// authenticationFailure classifies an authenticate error. Identity sentinels
// pass through; homeserver rate limits also report the retry hint on stderr.
func authenticationFailure(err error) error {
	for _, sentinel := range []error{errCredentialsMismatch, errDeviceMismatch, errCredentialsUnavailable, errLoginIdentityMismatch} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	if errors.Is(err, mautrix.MLimitExceeded) {
		var resp mautrix.RespError
		if errors.As(err, &resp) {
			if ms, ok := resp.ExtraData["retry_after_ms"].(float64); ok {
				fmt.Fprintf(os.Stderr, "retry_after_ms:%.0f\n", ms)
			}
		}
		return errCredentialsRateLimited
	}
	if errors.Is(err, mautrix.MForbidden) {
		return errCredentialsForbidden
	}
	return errCredentials
}

// startup succeeds once authentication and crypto initialisation have.
func startup() error {
	return nil
}

func (l labClient) syncRoom(ctx context.Context) error {
	var observed atomic.Bool
	syncer := l.cli.Syncer.(*failFastSyncer)
	syncCtx, cancelSync := context.WithCancel(ctx)
	defer cancelSync()
	var processed atomic.Bool
	syncer.processed = func() {
		processed.Store(true)
		cancelSync()
	}
	l.cli.Syncer.(mautrix.ExtensibleSyncer).OnSync(func(context.Context, *mautrix.RespSync, string) bool { observed.Store(true); return true })
	err := l.cli.SyncWithContext(syncCtx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return errSync
	}
	if !observed.Load() || !processed.Load() {
		return errSyncUnobserved
	}
	if err := preflightRoom(ctx, l.cli, l.room); err != nil {
		return errRoomState
	}
	return nil
}

func (l labClient) decrypt(ctx context.Context, o options) error {
	data, err := os.ReadFile(o.input)
	if err != nil {
		return errEventFile
	}
	var evt event.Event
	if parseEncryptedEvent(data, &evt) != nil {
		return errEventJSON
	}
	if evt.Type != event.EventEncrypted {
		return errDecryptMismatch
	}
	decrypted, err := l.helper.Decrypt(ctx, &evt)
	if err != nil {
		return errDecrypt
	}
	expectedData, err := os.ReadFile(o.expected)
	if err != nil {
		return errExpectedFile
	}
	var expected expectedMessage
	if json.Unmarshal(expectedData, &expected) != nil || expected.Body == "" || (expected.EventType != "m.sticker" && expected.Type == "") {
		return errExpectedJSON
	}
	if decrypted.RoomID != l.room || !matchesExpected(decrypted, expected) {
		return errDecryptMismatch
	}
	content := decrypted.Content.Parsed.(*event.MessageEventContent)
	if expected.SHA256 == "" {
		return nil
	}
	if content.URL != "" {
		return errMediaPlaintextURL
	}
	if content.File == nil {
		return errMediaMissing
	}
	return l.verifyMedia(ctx, content.File, expected.SHA256)
}

// verifyMedia downloads and decrypts an encrypted attachment and compares its
// plaintext SHA-256 with the expected hex digest.
func (l labClient) verifyMedia(ctx context.Context, file *event.EncryptedFileInfo, wantSHA256 string) error {
	uri, err := file.URL.Parse()
	if err != nil {
		return errMediaURI
	}
	resp, err := l.cli.Download(ctx, uri)
	if err != nil || resp == nil || resp.Body == nil {
		return errMediaDownload
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	_ = resp.Body.Close()
	if err != nil || len(data) > 4<<20 {
		return errMediaDownload
	}
	if file.DecryptInPlace(data) != nil {
		return errMediaDecrypt
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.ToLower(wantSHA256) {
		return errMediaMismatch
	}
	return nil
}

func (l labClient) sendText(ctx context.Context, o options) error {
	if reserveAttempt(o.receipt) != nil {
		return errAttemptReceipt
	}
	if err := preflightRoom(ctx, l.cli, l.room); err != nil {
		return errRoomState
	}
	body, err := os.ReadFile(o.input)
	if err != nil || len(bytes.TrimSpace(body)) == 0 || len(body) > 4<<10 {
		return errBodyFile
	}
	content := &event.MessageEventContent{MsgType: event.MsgText, Body: string(body)}
	resp, err := l.sendEncrypted(ctx, content)
	if err != nil {
		return err
	}
	if o.receipt != "" && (resp == nil || writeReceipt(o.receipt, string(resp.EventID)) != nil) {
		return errReceipt
	}
	return nil
}

func (l labClient) sendFile(ctx context.Context, o options) error {
	if reserveAttempt(o.receipt) != nil {
		return errAttemptReceipt
	}
	if err := preflightRoom(ctx, l.cli, l.room); err != nil {
		return errRoomState
	}
	plain, err := os.ReadFile(o.input)
	if err != nil || len(plain) == 0 || len(plain) > 4<<20 {
		return errFile
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(plain))
	if err != nil || format != "png" || config.Width != 64 || config.Height != 64 {
		return errFileShape
	}
	file := attachment.NewEncryptedFile()
	file.EncryptInPlace(plain)
	upload, err := l.cli.UploadMedia(ctx, mautrix.ReqUploadMedia{ContentBytes: plain, ContentType: "application/octet-stream", FileName: filepath.Base(o.input)})
	if err != nil {
		return errUpload
	}
	content := &event.MessageEventContent{MsgType: event.MsgImage, Body: filepath.Base(o.input), Info: &event.FileInfo{MimeType: "image/png", Width: 64, Height: 64, Size: len(plain)}, File: &event.EncryptedFileInfo{EncryptedFile: *file, URL: id.ContentURIString(upload.ContentURI.String())}}
	resp, err := l.sendEncrypted(ctx, content)
	if err != nil {
		return err
	}
	if resp == nil || writeReceipt(o.receipt, string(resp.EventID)) != nil {
		return errReceipt
	}
	return nil
}

// sendEncrypted Megolm-encrypts content for the portal room and sends it.
func (l labClient) sendEncrypted(ctx context.Context, content *event.MessageEventContent) (*mautrix.RespSendEvent, error) {
	encrypted, err := l.helper.Encrypt(ctx, l.room, event.EventMessage, content)
	if err != nil {
		return nil, errEncrypt
	}
	resp, err := l.cli.SendMessageEvent(ctx, l.room, event.EventEncrypted, encrypted)
	if err != nil {
		return nil, errSend
	}
	return resp, nil
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
			return errCredentialsMismatch
		}
		cli.AccessToken, cli.DeviceID = creds.Token, creds.Device
		who, err := cli.Whoami(ctx)
		if err != nil {
			return err
		}
		if who.UserID != creds.User || who.DeviceID != creds.Device {
			return errDeviceMismatch
		}
		return nil
	} else if _, statErr := os.Lstat(o.credentials); !os.IsNotExist(statErr) {
		return errCredentialsUnavailable
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
		return errLoginIdentityMismatch
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
	GeoURI    string          `json:"geo_uri,omitempty"`
	EventType string          `json:"event_type"`
	Type      string          `json:"type"`
	Body      string          `json:"body"`
	SHA256    string          `json:"sha256"`
	Info      *event.FileInfo `json:"info,omitempty"`
	FileName  string          `json:"filename,omitempty"`
}

func matchesExpected(evt *event.Event, expected expectedMessage) bool {
	typ := event.EventMessage
	if expected.EventType == "m.sticker" {
		typ = event.EventSticker
	} else if expected.EventType != "" && expected.EventType != "m.room.message" {
		return false
	}
	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if evt.Type != typ || !ok || string(content.MsgType) != expected.Type || content.Body != expected.Body {
		return false
	}
	if expected.GeoURI != "" && content.GeoURI != expected.GeoURI {
		return false
	}
	if expected.FileName != "" && content.FileName != expected.FileName {
		return false
	}
	if expected.Info != nil {
		actual, want := content.Info, expected.Info
		return actual != nil && actual.MimeType == want.MimeType && actual.Size == want.Size && actual.Width == want.Width && actual.Height == want.Height && actual.Duration == want.Duration
	}
	return true
}
