package connector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

const (
	testSelfID  int64 = 1000
	testOtherID int64 = 2000
	testChatID  int64 = 3000
)

type sentText struct {
	chatID  int64
	message string
}

type sentReply struct {
	request chat.ReplyRequest
}

type catchUpResult struct {
	events []events.Event
	err    error
}

type fakeKakao struct {
	mu                sync.Mutex
	connectErr        error
	resumeTargets     []syncmsg.Target
	resumeErr         error
	catchUps          map[int64]catchUpResult
	calls             []string
	stream            chan events.Result
	commits           []events.Event
	sends             []sentText
	replies           []sentReply
	sendResp          chat.WriteResponse
	sendErr           error
	closeCalls        int
	shutdownCalls     int
	shutdownFailures  int
	shutdownWait      <-chan struct{}
	shutdownEntered   chan struct{}
	shutdownEnterOnce sync.Once
	eventsEntered     chan struct{}
	eventsRelease     <-chan struct{}
	eventsEnterOnce   sync.Once
	catchupEntered    chan struct{}
	catchupRelease    <-chan struct{}
	catchupEnterOnce  sync.Once
	chatInfo          chatmeta.ChatInfoResponse
	chatInfoErr       error
	members           []chatmeta.Member
	membersErr        error
	memberList        chatmeta.MemberListResponse
	memberListErr     error
	metadataCalls     []string
}

func (f *fakeKakao) Connect(ctx context.Context) error { return f.connectErr }

func (f *fakeKakao) ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadataCalls = append(f.metadataCalls, fmt.Sprintf("ChatInfo(%d)", chatID))
	return f.chatInfo, f.chatInfoErr
}

func (f *fakeKakao) Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadataCalls = append(f.metadataCalls, fmt.Sprintf("Members(%d,%d)", chatID, len(userIDs)))
	return f.members, f.membersErr
}

func (f *fakeKakao) MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadataCalls = append(f.metadataCalls, fmt.Sprintf("MemberList(%d,%d)", chatID, token))
	return f.memberList, f.memberListErr
}

func (f *fakeKakao) Events(ctx context.Context) (<-chan events.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "Events")
	entered, release := f.eventsEntered, f.eventsRelease
	f.mu.Unlock()
	if entered != nil {
		f.eventsEnterOnce.Do(func() { close(entered) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.stream, nil
}

func (f *fakeKakao) ResumeTargets(ctx context.Context) ([]syncmsg.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ResumeTargets")
	return f.resumeTargets, f.resumeErr
}

func (f *fakeKakao) CatchUp(ctx context.Context, chatID, targetMax int64) ([]events.Event, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf("CatchUp(%d,%d)", chatID, targetMax))
	result := f.catchUps[chatID]
	entered, release := f.catchupEntered, f.catchupRelease
	f.mu.Unlock()
	if entered != nil {
		f.catchupEnterOnce.Do(func() { close(entered) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return result.events, result.err
}

func (f *fakeKakao) CommitEvent(evt events.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits = append(f.commits, evt)
	return nil
}

func (f *fakeKakao) SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, sentText{chatID: chatID, message: message})
	return f.sendResp, f.sendErr
}

func (f *fakeKakao) SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, sentReply{request: request})
	return f.sendResp, f.sendErr
}

func (f *fakeKakao) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

func (f *fakeKakao) Shutdown(ctx context.Context) error {
	f.mu.Lock()
	f.shutdownCalls++
	entered := f.shutdownEntered
	if f.shutdownFailures > 0 {
		f.shutdownFailures--
		f.mu.Unlock()
		return context.DeadlineExceeded
	}
	wait := f.shutdownWait
	f.mu.Unlock()
	if entered != nil {
		f.shutdownEnterOnce.Do(func() { close(entered) })
	}
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.Close()
}

func (f *fakeKakao) committed() []events.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]events.Event(nil), f.commits...)
}

// testHarness records everything the KakaoClient hands to the bridge.
type testHarness struct {
	mu      sync.Mutex
	queued  []bridgev2.RemoteEvent
	results []bridgev2.EventHandlingResult
	states  []status.BridgeState
}

func (h *testHarness) queue(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queued = append(h.queued, evt)
	if len(h.results) == 0 {
		return bridgev2.EventHandlingResultSuccess
	}
	result := h.results[0]
	h.results = h.results[1:]
	return result
}

func (h *testHarness) sendState(state status.BridgeState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.states = append(h.states, state)
}

func (h *testHarness) stateEvents() []status.BridgeStateEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]status.BridgeStateEvent, 0, len(h.states))
	for _, state := range h.states {
		out = append(out, state.StateEvent)
	}
	return out
}

func (h *testHarness) lastState() status.BridgeState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.states[len(h.states)-1]
}

func newTestClient(t *testing.T, open func() (kakaoClient, error)) (*KakaoClient, *testHarness) {
	t.Helper()
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)},
		Log:       zerolog.Nop(),
	}
	kc := newKakaoClient(login, testSelfID, open)
	harness := &testHarness{}
	kc.queue = harness.queue
	kc.sendState = harness.sendState
	return kc, harness
}

func convertedBody(t *testing.T, converted *bridgev2.ConvertedMessage) *event.MessageEventContent {
	t.Helper()
	if len(converted.Parts) != 1 {
		t.Fatalf("converted message has %d parts, want 1", len(converted.Parts))
	}
	return converted.Parts[0].Content
}

func TestGetChatInfoUsesSourceMetadataAndInitialRoster(t *testing.T) {
	fake := &fakeKakao{
		chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{
			ChatID:           testChatID,
			Type:             "DirectChat",
			DisplayNicknames: []string{"Ignored fallback"},
			Meta:             &chatmeta.RoomMeta{Name: "Source room"},
		}},
		memberList: chatmeta.MemberListResponse{Token: 9, MemberIDs: []int64{testSelfID, testOtherID}},
		members: []chatmeta.Member{
			{UserID: testOtherID, Nickname: "Source user"},
			{UserID: 9999, Nickname: "Unexpected"},
		},
	}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}

	info, err := kc.GetChatInfo(context.Background(), portal)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == nil || *info.Name != "Source room" {
		t.Fatalf("room name = %v, want source metadata", info.Name)
	}
	if !info.Members.IsFull || info.Members.TotalMemberCount != 2 {
		t.Fatalf("members = %+v, want complete initial roster", info.Members)
	}
	if info.Type == nil || *info.Type != database.RoomTypeDM || info.Members.OtherUserID != makeUserID(testOtherID) {
		t.Fatalf("direct metadata = type %v, other user %q", info.Type, info.Members.OtherUserID)
	}
	other, ok := info.Members.MemberMap[makeUserID(testOtherID)]
	if !ok || other.UserInfo == nil || other.UserInfo.Name == nil || *other.UserInfo.Name != "Source user" {
		t.Fatalf("other member = %+v, want source profile", other)
	}
	if _, ok := info.Members.MemberMap[makeUserID(9999)]; ok {
		t.Fatal("metadata admitted a profile outside the requested roster")
	}
	if got := fake.metadataCalls; fmt.Sprint(got) != "[ChatInfo(3000) MemberList(3000,0) Members(3000,2)]" {
		t.Fatalf("metadata calls = %v", got)
	}

	ghost := &bridgev2.Ghost{Ghost: &database.Ghost{ID: makeUserID(testOtherID)}}
	user, err := kc.GetUserInfo(context.Background(), ghost)
	if err != nil || user.Name == nil || *user.Name != "Source user" {
		t.Fatalf("cached user info = %+v, err = %v", user, err)
	}
}

func TestCatchUpMetadataCallbackUsesBootstrapOwner(t *testing.T) {
	stream := make(chan events.Result)
	missed := events.TextMessage{ChatID: testChatID, LogID: 41, AuthorID: testOtherID, Message: "missed"}
	fake := &fakeKakao{
		stream:        stream,
		resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 41}},
		catchUps:      map[int64]catchUpResult{testChatID: {events: []events.Event{missed}}},
		chatInfo:      chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Meta: &chatmeta.RoomMeta{Name: "Room"}}},
	}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if _, err := kc.GetChatInfo(context.Background(), &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}); err != nil {
			t.Errorf("metadata callback during catch-up: %v", err)
		}
		return bridgev2.EventHandlingResultSuccess
	}
	kc.Connect(context.Background())
	close(stream)
	waitForState(t, harness, status.StateTransientDisconnect)
}

func TestGetChatInfoKeepsDisplayOnlyRosterPartial(t *testing.T) {
	fake := &fakeKakao{
		chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{
			ChatID:            testChatID,
			ActiveMemberCount: 3,
			DisplayUserIDs:    []int64{testOtherID},
			DisplayNicknames:  []string{"Display user"},
		}},
		members: []chatmeta.Member{{UserID: testOtherID, Nickname: "Display user"}},
	}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}

	info, err := kc.GetChatInfo(context.Background(), portal)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == nil || *info.Name != "Display user" {
		t.Fatalf("room name = %v, want display nickname fallback", info.Name)
	}
	if info.Members.IsFull {
		t.Fatal("display-only IDs were marked as a complete roster")
	}
	if info.Members.TotalMemberCount != 3 {
		t.Fatalf("partial roster total = %d, want source active-member count", info.Members.TotalMemberCount)
	}
}

func TestGetChatInfoRejectsUnsupportedOrMismatchedRooms(t *testing.T) {
	for name, data := range map[string]chatmeta.ChatData{
		"mismatch":  {ChatID: testChatID + 1},
		"open chat": {ChatID: testChatID, LinkID: 44},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: data}}
			kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
			kc.client = fake
			portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}
			if _, err := kc.GetChatInfo(context.Background(), portal); err == nil {
				t.Fatal("GetChatInfo unexpectedly accepted unsupported room metadata")
			}
			if len(fake.metadataCalls) != 1 {
				t.Fatalf("metadata calls = %v, want CHATINFO only", fake.metadataCalls)
			}
		})
	}
}

func TestGetChatInfoRejectsInvalidRosterIDs(t *testing.T) {
	fake := &fakeKakao{
		chatInfo:   chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID}},
		memberList: chatmeta.MemberListResponse{MemberIDs: []int64{0, testOtherID}},
	}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}
	if _, err := kc.GetChatInfo(context.Background(), portal); !errors.Is(err, errInvalidMemberRoster) {
		t.Fatalf("error = %v, want invalid roster error", err)
	}
}

func TestCommittableOnlyWhenHandlingFinished(t *testing.T) {
	cases := []struct {
		name   string
		result bridgev2.EventHandlingResult
		want   bool
	}{
		{"success", bridgev2.EventHandlingResultSuccess, true},
		{"ignored duplicate", bridgev2.EventHandlingResultIgnored, true},
		{"queued", bridgev2.EventHandlingResultQueued, false},
		{"failed", bridgev2.EventHandlingResultFailed, false},
		{"success with error", bridgev2.EventHandlingResultSuccess.WithError(errors.New("boom")), false},
		{"backgrounded after timeout", bridgev2.EventHandlingResult{Queued: true, Success: true, Error: bridgev2.ErrHandlerBackgrounded}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := committable(tc.result); got != tc.want {
				t.Fatalf("committable = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestInboundTextIsBridgedThenCommitted(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	fake := &fakeKakao{}
	text := events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, SentAt: 1700000000, Message: "hello"}

	kc.handleEvent(fake, text)

	if len(harness.queued) != 1 {
		t.Fatalf("queued %d events, want 1", len(harness.queued))
	}
	msg, ok := harness.queued[0].(*simplevent.Message[events.TextMessage])
	if !ok {
		t.Fatalf("queued %T", harness.queued[0])
	}
	if msg.GetType() != bridgev2.RemoteEventMessage {
		t.Errorf("type = %v", msg.GetType())
	}
	if msg.GetPortalKey() != (networkid.PortalKey{ID: "3000", Receiver: "1000"}) {
		t.Errorf("portal key = %+v", msg.GetPortalKey())
	}
	if msg.GetID() != networkid.MessageID("3000:11") {
		t.Errorf("message ID = %q", msg.GetID())
	}
	if msg.GetSender() != (bridgev2.EventSender{Sender: "2000"}) {
		t.Errorf("sender = %+v", msg.GetSender())
	}
	if !msg.ShouldCreatePortal() {
		t.Error("message would not create its portal")
	}
	if !msg.GetTimestamp().Equal(time.Unix(1700000000, 0)) {
		t.Errorf("timestamp = %v", msg.GetTimestamp())
	}
	converted, err := msg.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := convertedBody(t, converted)
	if content.MsgType != event.MsgText || content.Body != "hello" {
		t.Errorf("content = %+v", content)
	}

	commits := fake.committed()
	if len(commits) != 1 || commits[0] != events.Event(text) {
		t.Fatalf("commits = %v", commits)
	}
}

func TestInboundMessageIsNotCommittedUnlessBridged(t *testing.T) {
	for _, result := range []bridgev2.EventHandlingResult{
		bridgev2.EventHandlingResultFailed,
		bridgev2.EventHandlingResultQueued,
		{Queued: true, Success: true, Error: bridgev2.ErrHandlerBackgrounded},
	} {
		kc, harness := newTestClient(t, nil)
		harness.results = []bridgev2.EventHandlingResult{result}
		fake := &fakeKakao{}

		kc.handleEvent(fake, events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "hello"})

		if commits := fake.committed(); len(commits) != 0 {
			t.Fatalf("result %+v committed %v; an unbridged message must stay replayable", result, commits)
		}
	}
}

func TestOwnMessageFromAnotherDeviceIsFromMe(t *testing.T) {
	kc, harness := newTestClient(t, nil)

	kc.handleEvent(&fakeKakao{}, events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testSelfID, Message: "from phone"})

	sender := harness.queued[0].GetSender()
	want := bridgev2.EventSender{IsFromMe: true, SenderLogin: "1000", Sender: "1000"}
	if sender != want {
		t.Fatalf("sender = %+v, want %+v", sender, want)
	}
}

func TestInboundReplyTargetsChatScopedMessage(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	fake := &fakeKakao{}
	reply := events.ReplyMessage{
		ChatID: testChatID, LogID: 13, AuthorID: testOtherID, SentAt: 1700000001, Message: "reply text",
		Source: events.ReplySource{LogID: 11, UserID: testSelfID, Message: "original"},
	}

	kc.handleEvent(fake, reply)

	msg := harness.queued[0].(*simplevent.Message[events.ReplyMessage])
	converted, err := msg.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if converted.ReplyTo == nil || converted.ReplyTo.MessageID != networkid.MessageID("3000:11") {
		t.Fatalf("reply target = %+v", converted.ReplyTo)
	}
	if content := convertedBody(t, converted); content.Body != "reply text" {
		t.Fatalf("body = %q", content.Body)
	}
	if len(fake.committed()) != 1 {
		t.Fatal("reply was not committed")
	}
}

func TestUnrenderedMessageKindsBecomeNoticesAndAreCommitted(t *testing.T) {
	photo := events.PhotoMessage{Message: media.PhotoMessage{ChatID: testChatID, LogID: 14}}
	unsupported := events.UnsupportedMessage{ChatID: testChatID, LogID: 15, Type: 99}

	for _, evt := range []events.Event{photo, unsupported} {
		kc, harness := newTestClient(t, nil)
		fake := &fakeKakao{}

		kc.handleEvent(fake, evt)

		if len(harness.queued) != 1 {
			t.Fatalf("%T queued %d events", evt, len(harness.queued))
		}
		msg := harness.queued[0].(*simplevent.Message[noticeData])
		if msg.GetSender() != (bridgev2.EventSender{}) {
			t.Errorf("%T sender = %+v, want the bridge bot", evt, msg.GetSender())
		}
		converted, err := msg.ConvertMessage(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if content := convertedBody(t, converted); content.MsgType != event.MsgNotice || content.Body == "" {
			t.Errorf("%T content = %+v", evt, content)
		}
		if len(fake.committed()) != 1 {
			t.Errorf("%T was not committed; skipping it would block later commits in its chat", evt)
		}
	}
}

func TestMetadataEventsAreNotQueuedOrCommitted(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	fake := &fakeKakao{}

	kc.handleEvent(fake, events.ReadStateChanged{ChatID: testChatID, UserID: testOtherID, Watermark: 11})
	kc.handleEvent(fake, events.UnknownPacket{Method: "SOMETHING"})

	if len(harness.queued) != 0 || len(fake.committed()) != 0 {
		t.Fatalf("queued %d, committed %d", len(harness.queued), len(fake.committed()))
	}
}

func waitForState(t *testing.T, harness *testHarness, want status.BridgeStateEvent) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		states := harness.stateEvents()
		if len(states) > 0 && states[len(states)-1] == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("states = %v, want last %s", harness.stateEvents(), want)
}

func TestConnectRunsEventLoopAndReportsSessionEnd(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result, 4)}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

	kc.Connect(context.Background())
	if !kc.IsLoggedIn() {
		t.Fatal("not logged in after connect")
	}
	fake.stream <- events.Result{Err: errors.New("malformed")}
	fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 21, AuthorID: testOtherID, Message: "one"}}
	close(fake.stream)

	waitForState(t, harness, status.StateTransientDisconnect)
	if got := harness.stateEvents(); got[0] != status.StateConnecting || got[1] != status.StateConnected {
		t.Fatalf("states = %v", got)
	}
	if harness.lastState().Error != stateDisconnected {
		t.Fatalf("error = %q", harness.lastState().Error)
	}
	if len(fake.committed()) != 1 {
		t.Fatalf("commits = %v", fake.committed())
	}
}

func TestKickoutReportsBadCredentials(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result, 2)}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

	kc.Connect(context.Background())
	fake.stream <- events.Result{Event: events.Kickout{Reason: 1}}
	close(fake.stream)

	waitForState(t, harness, status.StateBadCredentials)
	if harness.lastState().Error != stateKickedOut {
		t.Fatalf("error = %q", harness.lastState().Error)
	}
}

func TestChangeServerReportsDistinctTerminalDisconnect(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result, 2)}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

	kc.Connect(context.Background())
	fake.stream <- events.Result{Event: events.ChangeServer{}}
	close(fake.stream)

	waitForState(t, harness, status.StateTransientDisconnect)
	if got := harness.lastState().Error; got != stateChangeServer {
		t.Fatalf("error = %q, want %q", got, stateChangeServer)
	}
}

func TestTerminalNoticeStopsLaterEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event events.Event
		state status.BridgeStateEvent
		want  status.BridgeStateErrorCode
	}{
		{name: "change-server", event: events.ChangeServer{}, state: status.StateTransientDisconnect, want: stateChangeServer},
		{name: "kickout", event: events.Kickout{Reason: 7}, state: status.StateBadCredentials, want: stateKickedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeKakao{stream: make(chan events.Result, 3)}
			kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

			kc.Connect(context.Background())
			fake.stream <- events.Result{Event: tc.event}
			fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 22, AuthorID: testOtherID, Message: "after-terminal"}}
			fake.stream <- events.Result{Event: tc.event}
			close(fake.stream)

			waitForState(t, harness, tc.state)
			if got := harness.lastState().Error; got != tc.want {
				t.Fatalf("error = %q, want %q", got, tc.want)
			}
			if got := fake.committed(); len(got) != 0 {
				t.Fatalf("commits after terminal notice = %v, want none", got)
			}
		})
	}
}

func TestDisconnectClosesClientWithoutReportingFailure(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result)}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())

	// The real client closes its event stream when closed.
	go func() {
		for {
			fake.mu.Lock()
			closed := fake.closeCalls > 0
			fake.mu.Unlock()
			if closed {
				close(fake.stream)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	kc.Disconnect()

	if fake.closeCalls != 1 {
		t.Fatalf("close calls = %d", fake.closeCalls)
	}
	if fake.shutdownCalls != 1 {
		t.Fatalf("shutdown calls = %d", fake.shutdownCalls)
	}
	if kc.IsLoggedIn() {
		t.Fatal("still logged in after disconnect")
	}
	if last := harness.lastState().StateEvent; last != status.StateConnected {
		t.Fatalf("last state = %s; a requested disconnect is not a failure", last)
	}
}

func TestDisconnectRetainsCleanupOwnerAfterShutdownTimeout(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result), shutdownFailures: 1}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())
	go func() {
		for {
			fake.mu.Lock()
			closed := fake.closeCalls > 0
			fake.mu.Unlock()
			if closed {
				close(fake.stream)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	kc.Disconnect()
	kc.mu.Lock()
	retained := kc.cleanup != nil && kc.client == nil
	kc.mu.Unlock()
	if !retained {
		t.Fatal("timed-out shutdown did not retain cleanup owner")
	}
	if kc.IsLoggedIn() {
		t.Fatal("timed-out cleanup owner remained logged in")
	}
	kc.Connect(context.Background())
	kc.mu.Lock()
	if kc.client != nil {
		kc.mu.Unlock()
		t.Fatal("Connect admitted while cleanup owner was retained")
	}
	kc.mu.Unlock()

	kc.Disconnect()
	kc.mu.Lock()
	retained = kc.cleanup != nil
	kc.mu.Unlock()
	if retained {
		t.Fatal("successful retry retained cleanup owner")
	}
}

func TestConcurrentDisconnectRetriesRetainedOwner(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result), shutdownFailures: 1}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())
	go func() {
		for {
			fake.mu.Lock()
			closed := fake.closeCalls > 0
			fake.mu.Unlock()
			if closed {
				close(fake.stream)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); kc.Disconnect() }()
	}
	wg.Wait()
	fake.mu.Lock()
	shutdownCalls := fake.shutdownCalls
	fake.mu.Unlock()
	if shutdownCalls != 2 {
		t.Fatalf("shutdown calls = %d, want serialized retry", shutdownCalls)
	}
	kc.mu.Lock()
	retained := kc.cleanup != nil
	kc.mu.Unlock()
	if retained {
		t.Fatal("concurrent disconnect left cleanup owner retained after success")
	}
}

func TestConcurrentDisconnectSharesOverallTimeoutBudget(t *testing.T) {
	previous := terminalDisconnectTimeout
	terminalDisconnectTimeout = 50 * time.Millisecond
	t.Cleanup(func() { terminalDisconnectTimeout = previous })
	block := make(chan struct{})
	shutdownEntered := make(chan struct{})
	fake := &fakeKakao{stream: make(chan events.Result), shutdownWait: block, shutdownEntered: shutdownEntered}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())
	go kc.Disconnect()
	select {
	case <-shutdownEntered:
	case <-time.After(time.Second):
		t.Fatal("first shutdown did not start")
	}
	startedAt := time.Now()
	kc.Disconnect()
	if elapsed := time.Since(startedAt); elapsed > 80*time.Millisecond {
		t.Fatalf("second Disconnect exceeded shared timeout budget: %v", elapsed)
	}
	fake.mu.Lock()
	shutdownCalls := fake.shutdownCalls
	fake.mu.Unlock()
	if shutdownCalls > 2 {
		t.Fatalf("shutdown calls = %d, want at most one call per concurrent caller", shutdownCalls)
	}
	close(block)
	kc.Disconnect()
}

func TestConnectFailuresAreReportedWithoutRetry(t *testing.T) {
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return nil, errors.New("profile in use") })
	kc.Connect(context.Background())
	if last := harness.lastState(); last.StateEvent != status.StateUnknownError || last.Error != stateProfileUnavailable {
		t.Fatalf("open failure state = %+v", last)
	}

	opens := 0
	fake := &fakeKakao{connectErr: errors.New("network down")}
	kc, harness = newTestClient(t, func() (kakaoClient, error) {
		opens++
		return fake, nil
	})
	kc.Connect(context.Background())
	if last := harness.lastState(); last.StateEvent != status.StateTransientDisconnect || last.Error != stateConnectFailed {
		t.Fatalf("connect failure state = %+v", last)
	}
	if opens != 1 || fake.closeCalls != 1 {
		t.Fatalf("opens = %d, closes = %d; a failed connect must release the profile once and not retry", opens, fake.closeCalls)
	}
	if kc.IsLoggedIn() {
		t.Fatal("logged in after failed connect")
	}
}

func TestConnectFailureUsesShutdownForCleanup(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result), resumeErr: errors.New("bootstrap failed")}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())
	fake.mu.Lock()
	shutdownCalls := fake.shutdownCalls
	closeCalls := fake.closeCalls
	fake.mu.Unlock()
	if shutdownCalls != 1 {
		t.Fatalf("shutdown calls = %d, want one cleanup join", shutdownCalls)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want one shutdown-owned close", closeCalls)
	}
	if got := harness.lastState(); got.StateEvent != status.StateTransientDisconnect || got.Error != stateConnectFailed {
		t.Fatalf("connect failure state = %+v", got)
	}
}

func TestBootstrapShutdownTimeoutRetainsCleanupOwner(t *testing.T) {
	fake := &fakeKakao{resumeErr: errors.New("synthetic bootstrap failure"), shutdownFailures: 1}
	opens := 0
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opens++
		return fake, nil
	})
	kc.Connect(context.Background())
	kc.mu.Lock()
	retained := kc.cleanup == fake
	kc.mu.Unlock()
	if !retained {
		t.Fatal("bootstrap timeout lost cleanup owner")
	}
	kc.Connect(context.Background())
	if opens != 1 {
		t.Fatalf("Connect reopened profile during cleanup: opens = %d, want 1", opens)
	}
	kc.Disconnect()
	fake.mu.Lock()
	shutdownCalls := fake.shutdownCalls
	fake.mu.Unlock()
	if shutdownCalls != 2 {
		t.Fatalf("shutdown retry calls = %d, want 2", shutdownCalls)
	}
}

func TestDisconnectOwnsBootstrapSubscriptionBeforeEventsReturns(t *testing.T) {
	release := make(chan struct{})
	eventsEntered := make(chan struct{})
	fake := &fakeKakao{
		stream:        make(chan events.Result),
		eventsEntered: eventsEntered,
		eventsRelease: release,
		shutdownWait:  nil,
	}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	connectDone := make(chan struct{})
	go func() {
		kc.Connect(context.Background())
		close(connectDone)
	}()
	select {
	case <-eventsEntered:
	case <-time.After(time.Second):
		t.Fatal("Events did not block")
	}
	kc.Disconnect()
	fake.mu.Lock()
	shutdownCalls := fake.shutdownCalls
	fake.mu.Unlock()
	if shutdownCalls != 1 {
		t.Fatalf("shutdown calls = %d, want one Disconnect-owned shutdown", shutdownCalls)
	}
	close(release)
	select {
	case <-connectDone:
	case <-time.After(time.Second):
		t.Fatal("Connect did not unwind after Events release")
	}
}

func TestDisconnectDoesNotAdmitReplacementDuringBootstrap(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	fake := &fakeKakao{
		stream:         make(chan events.Result),
		resumeTargets:  []syncmsg.Target{{ChatID: testChatID, MaxLogID: 1}},
		catchupEntered: entered,
		catchupRelease: release,
	}
	opens := 0
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opens++
		return fake, nil
	})
	connectDone := make(chan struct{})
	go func() {
		kc.Connect(context.Background())
		close(connectDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("bootstrap catch-up did not block")
	}
	kc.Disconnect()
	kc.Connect(context.Background())
	if opens != 1 {
		t.Fatalf("replacement opened during old bootstrap: opens=%d, want 1", opens)
	}
	close(release)
	select {
	case <-connectDone:
	case <-time.After(time.Second):
		t.Fatal("old bootstrap did not unwind")
	}
	if kc.IsLoggedIn() {
		t.Fatal("stale bootstrap installed a client after Disconnect")
	}
}

func connectedClient(t *testing.T, fake *fakeKakao) *KakaoClient {
	t.Helper()
	fake.stream = make(chan events.Result)
	t.Cleanup(func() { close(fake.stream) })
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())
	return kc
}

func matrixMessage(msgType event.MessageType, body string) *bridgev2.MatrixMessage {
	msg := &bridgev2.MatrixMessage{}
	msg.Content = &event.MessageEventContent{MsgType: msgType, Body: body}
	msg.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, "1000")}}
	return msg
}

func TestOutboundTextIsSentOnceAndRecorded(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{ChatID: testChatID, LogID: 31, SendAt: 1700000002}}
	kc := connectedClient(t, fake)

	resp, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "hi there"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.sends) != 1 || fake.sends[0] != (sentText{chatID: testChatID, message: "hi there"}) {
		t.Fatalf("sends = %+v", fake.sends)
	}
	if resp.DB.ID != networkid.MessageID("3000:31") || resp.DB.SenderID != networkid.UserID("1000") {
		t.Fatalf("db message = %+v", resp.DB)
	}
	if !resp.DB.Timestamp.Equal(time.Unix(1700000002, 0)) {
		t.Fatalf("timestamp = %v", resp.DB.Timestamp)
	}
}

func TestOutboundEmoteIsPrefixed(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 32}}
	kc := connectedClient(t, fake)

	if _, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgEmote, "waves")); err != nil {
		t.Fatal(err)
	}
	if fake.sends[0].message != "* waves" {
		t.Fatalf("sent %q", fake.sends[0].message)
	}
}

func TestInboundMessagePersistsReplyMetadata(t *testing.T) {
	kc, harness := newTestClient(t, nil)
	kc.handleEvent(&fakeKakao{}, events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "source"})
	msg := harness.queued[0].(*simplevent.Message[events.TextMessage])
	converted, err := msg.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
	if !ok || metadata.ChatID != testChatID || metadata.LogID != 12 || metadata.AuthorID != testOtherID || metadata.Type != chat.TextType || metadata.Preview != "source" {
		t.Fatalf("metadata = %+v", converted.Parts[0].DBMetadata)
	}
}

func TestOutboundReplyUsesStoredSourceMetadata(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{ChatID: testChatID, LogID: 33, SendAt: 1700000003}}
	kc := connectedClient(t, fake)
	msg := matrixMessage(event.MsgText, "answer")
	msg.ReplyTo = &database.Message{
		ID:       makeMessageID(testChatID, 11),
		Room:     makePortalKey(testChatID, makeUserLoginID(testSelfID)),
		SenderID: makeUserID(testOtherID),
		Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, chat.TextType, "source", 0),
	}
	resp, err := kc.HandleMatrixMessage(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.sends) != 0 || len(fake.replies) != 1 {
		t.Fatalf("text sends = %v, replies = %v", fake.sends, fake.replies)
	}
	target := fake.replies[0].request.Target
	if target != (chat.ReplyTarget{LogID: 11, UserID: testOtherID, Type: chat.TextType, Message: "source"}) {
		t.Fatalf("reply target = %+v", target)
	}
	if resp.DB.Metadata.(*KakaoMessageMetadata).Type != chat.ReplyType {
		t.Fatalf("outbound metadata = %+v", resp.DB.Metadata)
	}
}

func TestOutboundEmoteReplyPreservesBodyAndUsesReplyOnce(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 34}}
	kc := connectedClient(t, fake)
	msg := matrixMessage(event.MsgEmote, "waves")
	msg.ReplyTo = &database.Message{
		ID:       makeMessageID(testChatID, 11),
		Room:     makePortalKey(testChatID, makeUserLoginID(testSelfID)),
		SenderID: makeUserID(testOtherID),
		Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, chat.ReplyType, "reply source", 0),
	}
	if _, err := kc.HandleMatrixMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(fake.replies) != 1 || fake.replies[0].request.Message != "* waves" {
		t.Fatalf("replies = %+v", fake.replies)
	}
}

func TestOutboundReplyRejectsOldCrossChatAndUnresolvedTargets(t *testing.T) {
	cases := []struct {
		name  string
		reply *database.Message
		want  error
	}{
		{name: "old row", reply: &database.Message{ID: makeMessageID(testChatID, 11), Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID)}, want: errMissingReplyMetadata},
		{name: "cross chat", reply: &database.Message{ID: makeMessageID(4000, 11), Room: makePortalKey(4000, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID), Metadata: newKakaoMessageMetadata(4000, 11, testOtherID, chat.TextType, "source", 0)}, want: errCrossChatReply},
		{name: "sender mismatch", reply: &database.Message{ID: makeMessageID(testChatID, 11), Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(9999), Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, chat.TextType, "source", 0)}, want: errReplySenderMismatch},
		{name: "receiver mismatch", reply: &database.Message{ID: makeMessageID(testChatID, 11), Room: makePortalKey(testChatID, makeUserLoginID(9999)), SenderID: makeUserID(testOtherID), Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, chat.TextType, "source", 0)}, want: errCrossChatReply},
		{name: "empty room id wrong receiver", reply: &database.Message{ID: makeMessageID(testChatID, 11), Room: networkid.PortalKey{Receiver: makeUserLoginID(9999)}, SenderID: makeUserID(testOtherID), Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, chat.TextType, "source", 0)}, want: errCrossChatReply},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 35}}
			kc := connectedClient(t, fake)
			msg := matrixMessage(event.MsgText, "answer")
			msg.ReplyTo = tc.reply
			_, err := kc.HandleMatrixMessage(context.Background(), msg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if len(fake.sends) != 0 || len(fake.replies) != 0 {
				t.Fatalf("sent unresolved reply: sends=%v replies=%v", fake.sends, fake.replies)
			}
		})
	}
}

func TestOutboundReplyRejectsUnresolvedMatrixRelation(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 36}}
	kc := connectedClient(t, fake)
	msg := matrixMessage(event.MsgText, "answer")
	msg.Content.RelatesTo = &event.RelatesTo{}
	msg.Content.RelatesTo.SetReplyTo("$missing")
	if _, err := kc.HandleMatrixMessage(context.Background(), msg); !errors.Is(err, errMissingReplyMetadata) {
		t.Fatalf("error = %v, want unresolved reply error", err)
	}
	if len(fake.sends) != 0 || len(fake.replies) != 0 {
		t.Fatal("unresolved relation was sent")
	}
}

func TestOutboundReplyFailureIsAmbiguousAndNotRetried(t *testing.T) {
	sendErr := errors.New("connection reset during reply")
	fake := &fakeKakao{sendErr: sendErr}
	kc := connectedClient(t, fake)
	msg := matrixMessage(event.MsgText, "answer")
	msg.ReplyTo = &database.Message{
		ID:       makeMessageID(testChatID, 11),
		Room:     makePortalKey(testChatID, makeUserLoginID(testSelfID)),
		SenderID: makeUserID(testOtherID),
		Metadata: newKakaoMessageMetadata(testChatID, 11, testOtherID, chat.TextType, "source", 0),
	}
	if _, err := kc.HandleMatrixMessage(context.Background(), msg); !errors.Is(err, sendErr) {
		t.Fatalf("error = %v", err)
	}
	if len(fake.replies) != 1 {
		t.Fatalf("reply attempts = %d, want one", len(fake.replies))
	}
}

func TestOutboundSendFailureIsNotRetried(t *testing.T) {
	sendErr := errors.New("connection reset during write")
	fake := &fakeKakao{sendErr: sendErr}
	kc := connectedClient(t, fake)

	_, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "maybe delivered"))
	if !errors.Is(err, sendErr) {
		t.Fatalf("error = %v", err)
	}
	if len(fake.sends) != 1 {
		t.Fatalf("sent %d times; an ambiguous send must not be retried", len(fake.sends))
	}
}

func TestOutboundRejectsWhatCannotBeSent(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	if _, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgImage, "photo.jpg")); !errors.Is(err, bridgev2.ErrUnsupportedMessageType) {
		t.Fatalf("image error = %v", err)
	}

	fake = &fakeKakao{sendResp: chat.WriteResponse{LogID: 0}}
	kc = connectedClient(t, fake)
	if _, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "hi")); err == nil {
		t.Fatal("accepted a send response without a log ID")
	}

	disconnected, _ := newTestClient(t, nil)
	if _, err := disconnected.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "hi")); !errors.Is(err, bridgev2.ErrNotLoggedIn) {
		t.Fatalf("disconnected error = %v", err)
	}
}

func TestOverlappingConnectOpensProfileOnce(t *testing.T) {
	release := make(chan struct{})
	opens := 0
	var opensMu sync.Mutex
	fake := &fakeKakao{stream: make(chan events.Result)}
	t.Cleanup(func() { close(fake.stream) })
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opensMu.Lock()
		opens++
		opensMu.Unlock()
		<-release
		return fake, nil
	})

	first := make(chan struct{})
	go func() {
		kc.Connect(context.Background())
		close(first)
	}()
	for {
		opensMu.Lock()
		started := opens == 1
		opensMu.Unlock()
		if started {
			break
		}
		time.Sleep(time.Millisecond)
	}
	kc.Connect(context.Background())
	close(release)
	<-first

	if opens != 1 {
		t.Fatalf("opened the profile %d times", opens)
	}
}

func TestDisconnectDuringConnectReleasesProfile(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result)}
	t.Cleanup(func() { close(fake.stream) })
	var kc *KakaoClient
	kc, _ = newTestClient(t, func() (kakaoClient, error) {
		kc.Disconnect()
		return fake, nil
	})

	kc.Connect(context.Background())

	if kc.IsLoggedIn() {
		t.Fatal("logged in although disconnect was requested during connect")
	}
	if fake.closeCalls != 1 {
		t.Fatalf("close calls = %d, want the opened client released once", fake.closeCalls)
	}
}

// Regression: after a bridge restart the framework calls LoadUserLogin before
// creating the login's BridgeState queue. Binding the queue at construction
// captured nil, so every later state was silently dropped.
func TestBridgeStateQueueIsResolvedAtSendTime(t *testing.T) {
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)},
		Log:       zerolog.Nop(),
	}
	kc := newKakaoClient(login, testSelfID, nil)

	queue := &bridgev2.BridgeStateQueue{}
	login.BridgeState = queue

	if kc.stateQueue() != queue {
		t.Fatal("client kept the bridge-state queue that existed when it was constructed")
	}
}

func TestConnectCatchesUpMissedMessagesBeforeSubscribingToLiveEvents(t *testing.T) {
	missedOne := events.TextMessage{ChatID: testChatID, LogID: 41, AuthorID: testOtherID, Message: "missed one"}
	missedTwo := events.TextMessage{ChatID: testChatID, LogID: 42, AuthorID: testOtherID, Message: "missed two"}
	live := events.TextMessage{ChatID: testChatID, LogID: 43, AuthorID: testOtherID, Message: "live"}
	fake := &fakeKakao{
		stream:        make(chan events.Result, 1),
		resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 42}},
		catchUps:      map[int64]catchUpResult{testChatID: {events: []events.Event{missedOne, missedTwo}}},
	}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

	kc.Connect(context.Background())
	fake.stream <- events.Result{Event: live}
	close(fake.stream)
	waitForState(t, harness, status.StateTransientDisconnect)

	wantCalls := []string{"ResumeTargets", "CatchUp(3000,42)", "Events"}
	if fmt.Sprint(fake.calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("calls = %v, want %v", fake.calls, wantCalls)
	}
	wantCommits := []events.Event{missedOne, missedTwo, live}
	if fmt.Sprint(fake.committed()) != fmt.Sprint(wantCommits) {
		t.Fatalf("commits = %v, want missed messages before the live one", fake.committed())
	}
	if got := harness.stateEvents(); got[1] != status.StateConnected {
		t.Fatalf("states = %v", got)
	}
}

func TestUnrecoverableGapPostsNoticeAndKeepsConnecting(t *testing.T) {
	fake := &fakeKakao{
		stream:        make(chan events.Result),
		resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 42}},
		catchUps:      map[int64]catchUpResult{testChatID: {err: client.ErrGapUnresolved}},
	}
	t.Cleanup(func() { close(fake.stream) })
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

	kc.Connect(context.Background())

	if !kc.IsLoggedIn() || harness.lastState().StateEvent != status.StateConnected {
		t.Fatalf("logged in = %t, states = %v", kc.IsLoggedIn(), harness.stateEvents())
	}
	if len(harness.queued) != 1 {
		t.Fatalf("queued %d events, want one gap notice", len(harness.queued))
	}
	notice := harness.queued[0].(*simplevent.Message[string])
	if notice.GetID() != networkid.MessageID("gap:3000:42") || notice.GetPortalKey().ID != "3000" {
		t.Fatalf("notice ID = %q, portal = %+v", notice.GetID(), notice.GetPortalKey())
	}
	converted, err := notice.ConvertMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if content := convertedBody(t, converted); content.MsgType != event.MsgNotice {
		t.Fatalf("content = %+v", content)
	}
	if len(fake.committed()) != 0 {
		t.Fatalf("committed %v for an unrecovered gap", fake.committed())
	}
}

func TestCatchUpFailureAbortsConnectBeforeLiveEvents(t *testing.T) {
	for name, fake := range map[string]*fakeKakao{
		"targets": {resumeErr: errors.New("targets failed")},
		"catch-up": {
			resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 42}},
			catchUps:      map[int64]catchUpResult{testChatID: {err: errors.New("connection reset")}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })

			kc.Connect(context.Background())

			if kc.IsLoggedIn() {
				t.Fatal("logged in although catch-up failed")
			}
			if last := harness.lastState(); last.StateEvent != status.StateTransientDisconnect || last.Error != stateConnectFailed {
				t.Fatalf("state = %+v", last)
			}
			for _, call := range fake.calls {
				if call == "Events" {
					t.Fatal("subscribed to live events after a failed catch-up; live commits could skip missed messages")
				}
			}
			if fake.closeCalls != 1 {
				t.Fatalf("close calls = %d", fake.closeCalls)
			}
		})
	}
}
