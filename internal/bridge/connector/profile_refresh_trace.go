package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/rs/zerolog"
)

// profileRefreshTrace records the source reads of one group profile refresh,
// so a failure can name the step that failed, how long it waited for account
// admission and how long it took. It records only command names, durations
// and a fixed error class, never a server response.
type profileRefreshTrace struct {
	kakaoClient
	start time.Time
	mu    sync.Mutex
	steps []profileRefreshStep
}

type profileRefreshStep struct {
	command string
	startAt time.Duration
	wait    time.Duration
	took    time.Duration
	class   string
}

func newProfileRefreshTrace(c kakaoClient) *profileRefreshTrace {
	return &profileRefreshTrace{kakaoClient: c, start: time.Now()}
}

func traceStep[T any](t *profileRefreshTrace, ctx context.Context, command string, read func(context.Context) (T, error)) (T, error) {
	started := time.Now()
	var wait time.Duration
	var waitMu sync.Mutex
	ctx = client.WithAdmissionObserver(ctx, func(_ string, d time.Duration) {
		waitMu.Lock()
		wait += d
		waitMu.Unlock()
	})
	value, err := read(ctx)
	waitMu.Lock()
	step := profileRefreshStep{command: command, startAt: started.Sub(t.start), wait: wait, took: time.Since(started), class: sourceErrorClass(err)}
	waitMu.Unlock()
	t.mu.Lock()
	t.steps = append(t.steps, step)
	t.mu.Unlock()
	return value, err
}

func (t *profileRefreshTrace) ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error) {
	return traceStep(t, ctx, "CHATINFO", func(ctx context.Context) (chatmeta.ChatInfoResponse, error) {
		return t.kakaoClient.ChatInfo(ctx, chatID)
	})
}

func (t *profileRefreshTrace) GetMetas(ctx context.Context, chatIDs []int64) ([]chatmeta.ChatMetaSet, error) {
	return traceStep(t, ctx, "GETMETAS", func(ctx context.Context) ([]chatmeta.ChatMetaSet, error) {
		return t.kakaoClient.GetMetas(ctx, chatIDs)
	})
}

func (t *profileRefreshTrace) PersonalMeta(ctx context.Context, chatID int64) (*chatmeta.RoomMeta, error) {
	return traceStep(t, ctx, "PERSONALMETA", func(ctx context.Context) (*chatmeta.RoomMeta, error) {
		return t.kakaoClient.PersonalMeta(ctx, chatID)
	})
}

func (t *profileRefreshTrace) MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error) {
	return traceStep(t, ctx, "GETMOMETA", func(ctx context.Context) (chatmeta.MoimResponse, error) {
		return t.kakaoClient.MoimMeta(ctx, chatID)
	})
}

func (t *profileRefreshTrace) Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error) {
	return traceStep(t, ctx, "MEMBER", func(ctx context.Context) ([]chatmeta.Member, error) {
		return t.kakaoClient.Members(ctx, chatID, userIDs)
	})
}

func (t *profileRefreshTrace) MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error) {
	return traceStep(t, ctx, "MEMLIST", func(ctx context.Context) (chatmeta.MemberListResponse, error) {
		return t.kakaoClient.MemberList(ctx, chatID, token)
	})
}

func (t *profileRefreshTrace) ChatOnRoom(ctx context.Context, chatID int64) (chatmeta.ChatOnRoomResponse, error) {
	return traceStep(t, ctx, "CHATONROOM", func(ctx context.Context) (chatmeta.ChatOnRoomResponse, error) {
		return t.kakaoClient.ChatOnRoom(ctx, chatID)
	})
}

// MarshalZerologArray logs the steps in call order.
func (t *profileRefreshTrace) MarshalZerologArray(a *zerolog.Array) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.steps {
		a.Dict(zerolog.Dict().Str("command", s.command).
			Int64("start_ms", s.startAt.Milliseconds()).Int64("admission_wait_ms", s.wait.Milliseconds()).
			Int64("took_ms", s.took.Milliseconds()).Str("error_class", s.class))
	}
}

// sourceErrorClass names the kind of a source request failure without
// including any text from the server. It returns "" for nil.
func sourceErrorClass(err error) string {
	var status client.StatusError
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, client.ErrClosed), errors.Is(err, client.ErrClientClosed):
		return "session_closed"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.As(err, &status):
		return fmt.Sprintf("status_%d", status.Status)
	case errors.Is(err, client.ErrHistoryBusy):
		return "admission_busy"
	case errors.Is(err, client.ErrProtocol):
		return "protocol"
	case errors.As(err, &netErr):
		return "network"
	}
	return "other"
}

func (t *profileRefreshTrace) PersonalMetas(ctx context.Context) (map[int64]chatmeta.RoomMeta, error) {
	return traceStep(t, ctx, "GETMCMETA", func(ctx context.Context) (map[int64]chatmeta.RoomMeta, error) {
		return t.kakaoClient.PersonalMetas(ctx)
	})
}
