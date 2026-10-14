package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestSyncReactionsCommandUsesOwnedRoomAndSharedGates(t *testing.T) {
	for _, tc := range []struct {
		name, typ, reply                   string
		foreign, noPortal, extraArgs, stop bool
		want                               int
	}{
		{name: "supported", typ: "MultiChat", reply: "sync complete", want: 1},
		{name: "business", typ: "PlusChat", reply: "unsupported"},
		{name: "stopped", typ: "MultiChat", stop: true, reply: "disabled"},
		{name: "foreign", typ: "MultiChat", foreign: true, reply: "own Kakao"},
		{name: "no-room", noPortal: true, reply: "in the Kakao room"},
		{name: "extra-args", extraArgs: true, reply: "in the Kakao room"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := &recordingIntent{}
			kc, portal, _, _ := newFrameworkReactionFixture(t, intent)
			ctx := context.Background()
			user, err := kc.login.Bridge.GetUserByMXID(ctx, "@owner:test")
			if err != nil {
				t.Fatal(err)
			}
			_, err = user.NewLogin(ctx, &database.UserLogin{ID: kc.login.ID, Metadata: &UserLoginMetadata{Profile: "synthetic"}}, &bridgev2.NewLoginParams{LoadUserLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
				kc.login = login
				login.Client = kc
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			backend := reactionViewingBackend()
			backend.chatInfo.ChatData.Type = tc.typ
			backend.chatOnRoom[testChatID] = chatmeta.ChatOnRoomResponse{ChatID: testChatID, StopSyncLogMeta: tc.stop}
			kc.client = backend
			now := time.Unix(100, 0)
			kc.now = func() time.Time { return now }
			ce := &commands.Event{Bridge: kc.login.Bridge, User: user, Portal: portal, Bot: intent, Ctx: ctx, Log: &kc.login.Log, OrigRoomID: portal.MXID}
			if tc.foreign {
				portal.Receiver = makeUserLoginID(9000)
			}
			if tc.noPortal {
				ce.Portal = nil
			}
			if tc.extraArgs {
				ce.Args = []string{"all"}
			}
			commandSyncReactions.Func(ce)
			if len(backend.reactionCursors) != tc.want {
				t.Fatalf("metadata calls=%v, want %d", backend.reactionCursors, tc.want)
			}
			if len(intent.messages) != 1 {
				t.Fatalf("reply count=%d", len(intent.messages))
			}
			body := intent.messages[0].content.AsMessage().Body
			if !strings.Contains(body, tc.reply) {
				t.Fatalf("reply=%q, want %q", body, tc.reply)
			}
			if tc.foreign || tc.noPortal || tc.extraArgs {
				if len(backend.metadataCalls) != 0 {
					t.Fatal("invalid command accessed source")
				}
				return
			}
			before := len(backend.metadataCalls)
			commandSyncReactions.Func(ce)
			if len(backend.metadataCalls) != before {
				t.Fatal("command bypassed cooldown")
			}
			if !strings.Contains(intent.messages[1].content.AsMessage().Body, "ten seconds") {
				t.Fatal("cooldown not reported")
			}
			now = now.Add(10 * time.Second)
			commandSyncReactions.Func(ce)
			if len(backend.metadataCalls) != before+1 {
				t.Fatal("explicit command did not refresh eligibility")
			}
		})
	}
}
