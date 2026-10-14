package connector

import (
	"errors"

	"maunium.net/go/mautrix/bridgev2/commands"
)

var commandSyncReactions = &commands.FullHandler{
	Name:          "sync-reactions",
	Help:          commands.HelpMeta{Section: commands.HelpSectionMisc, Description: "Recover missed reactions in this Kakao room"},
	RequiresLogin: true,
	Func: func(ce *commands.Event) {
		if ce.Portal == nil || len(ce.Args) != 0 {
			ce.Reply("Run `$cmdprefix sync-reactions` in the Kakao room you want to sync.")
			return
		}
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("No Kakao profile is configured.")
			return
		}
		kc, ok := login.Client.(*KakaoClient)
		if !ok || login.UserMXID != ce.User.MXID || ce.Portal.Receiver != login.ID {
			ce.Reply("Select your own Kakao login for this room.")
			return
		}
		requested, err := kc.syncRoomReactions(ce.Ctx, ce.Portal)
		switch {
		case errors.Is(err, errReactionSyncBusy):
			ce.Reply("A reaction sync is running or just finished. Try again after ten seconds.")
		case errors.Is(err, errReactionSyncBudget):
			ce.Reply("This batch is complete. Run `$cmdprefix sync-reactions` again after ten seconds to continue.")
		case err != nil:
			ce.Reply("Reaction sync stopped; its cursor was retained. Check the connection before trying again.")
		case !requested:
			ce.Reply("Reaction metadata sync is disabled or unsupported for this room.")
		default:
			ce.Reply("Reaction metadata sync complete.")
		}
	},
}
