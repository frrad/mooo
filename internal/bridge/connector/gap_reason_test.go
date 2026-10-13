package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/protocol/events"
)

// A malformed_payload gap reports the decoder's reason in the notice, the
// stored metadata and a warning whose fields identify no account, chat or
// message, so the line can be quoted in evidence.
func TestMessageGapReportsReasonWithoutIdentifiers(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	var logs bytes.Buffer
	kc.login.Bridge = &bridgev2.Bridge{Log: zerolog.New(&logs)}
	gap := events.MessageGap{ChatID: testChatID, LogID: 100, AuthorID: testOtherID, SentAt: 1700000000, Type: 2, Reason: "thumbnail_url_host"}

	if !kc.handleEvent(&fakeKakao{}, gap) {
		t.Fatal("message gap was not handled")
	}
	queued := harness.queued[0].(*simplevent.Message[noticeData])
	converted, err := queued.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if body := convertedBody(t, converted).Body; body != "A KakaoTalk message could not be displayed (malformed_payload: thumbnail_url_host)." {
		t.Fatalf("notice body = %q", body)
	}
	if meta := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata); meta.ConversionGap != "malformed_payload" || meta.GapReason != "thumbnail_url_host" {
		t.Fatalf("metadata = %#v", meta)
	}

	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("want exactly one JSON log line, got %q: %v", logs.String(), err)
	}
	want := map[string]any{
		"level":      "warn",
		"kakao_type": float64(2),
		"gap_reason": "thumbnail_url_host",
		"message":    "Kakao message content failed decoding; bridged as a malformed_payload notice",
	}
	if len(line) != len(want) {
		t.Fatalf("log fields = %v, want only %v", line, want)
	}
	for key, value := range want {
		if line[key] != value {
			t.Fatalf("log field %s = %v, want %v", key, line[key], value)
		}
	}
	for _, id := range []int64{testChatID, testOtherID, testSelfID, 100} {
		if bytes.Contains(logs.Bytes(), []byte(strconv.FormatInt(id, 10))) {
			t.Fatalf("log line contains identifier %d: %s", id, logs.String())
		}
	}
}
