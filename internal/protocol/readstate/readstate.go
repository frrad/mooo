// Package readstate contains pure read-state transition helpers. It does not
// persist state or issue network requests; callers apply the planned result.
package readstate

const (
	EligibleUnreadExcludedType   int32 = 10001
	EligibleUnreadExcludedStatus int32 = 5
)

type StoredLog struct {
	ChatID int64
	LogID  int64
	Type   int32
	Status int32
	Scope  int32
}

// CountEligibleUnread applies the reviewed unread-log predicate exactly.
func CountEligibleUnread(logs []StoredLog, chatID, lowerBound int64) int64 {
	var count int64
	for _, log := range logs {
		if log.ChatID != chatID || log.LogID <= 0 || log.LogID <= lowerBound ||
			log.Type == EligibleUnreadExcludedType || log.Status == EligibleUnreadExcludedStatus ||
			(log.Scope != 1 && log.Scope != 3) {
			continue
		}
		count++
	}
	return count
}

type Inputs struct{ EligibleUnreadCount int64 }
type Notice struct {
	ChatID, UserID, Watermark int64
}
type State struct {
	RoomExists          bool
	CurrentUserID       int64
	CountOfNewMessage   int64
	LastLogID           int64
	LastSeenLogID       int64
	MentionReplyPresent bool
	MemberWatermarks    map[int64]int64
	ActiveMemberIDs     []int64
	ActiveMemberCount   int
	BotIDs              map[int64]bool
	RoomType            int32
	Frozen              bool
}
type Effect struct {
	Kind           string  `json:"kind"`
	ChatID         int64   `json:"chatId,omitempty"`
	UserID         int64   `json:"userId,omitempty"`
	Watermark      int64   `json:"watermark,omitempty"`
	Count          int64   `json:"count,omitempty"`
	LowerBound     int64   `json:"lowerBound,omitempty"`
	ExcludedType   int32   `json:"excludedType,omitempty"`
	ExcludedStatus int32   `json:"excludedStatus,omitempty"`
	AllowedScopes  []int32 `json:"allowedScopes,omitempty"`
}
type Transition struct {
	State   State
	Applied bool
	Effects []Effect
}

func ReduceDECUNREAD(input State, notice Notice, inputs Inputs) Transition {
	if !input.RoomExists {
		return Transition{State: cloneState(input), Effects: []Effect{}}
	}
	out := cloneState(input)
	effects := make([]Effect, 0, 8)
	if notice.UserID == input.CurrentUserID {
		if input.CountOfNewMessage > 0 {
			if notice.Watermark < input.LastLogID {
				lower := input.LastSeenLogID
				if lower < notice.Watermark {
					lower = notice.Watermark
				}
				effects = append(effects, Effect{Kind: "query_unread", ChatID: notice.ChatID, LowerBound: lower, ExcludedType: EligibleUnreadExcludedType, ExcludedStatus: EligibleUnreadExcludedStatus, AllowedScopes: []int32{1, 3}})
				out.CountOfNewMessage = inputs.EligibleUnreadCount
				effects = append(effects, Effect{Kind: "set_unread", Count: out.CountOfNewMessage})
			} else {
				out.CountOfNewMessage = 0
				effects = append(effects, Effect{Kind: "clear_unread"}, Effect{Kind: "reset_mention_reply"})
				out.MentionReplyPresent = false
			}
		}
		effects = append(effects, Effect{Kind: "check_joined"}, Effect{Kind: "archive_refresh"})
	}
	if !helperSuppressed(out, notice.UserID) {
		if out.MemberWatermarks == nil {
			out.MemberWatermarks = make(map[int64]int64)
		}
		added := !containsID(out.ActiveMemberIDs, notice.UserID)
		if added {
			out.ActiveMemberIDs = append(out.ActiveMemberIDs, notice.UserID)
			effects = append(effects, Effect{Kind: "active_member_add", UserID: notice.UserID})
		}
		old, ok := out.MemberWatermarks[notice.UserID]
		changedWatermark := !ok || notice.Watermark > old
		if changedWatermark {
			out.MemberWatermarks[notice.UserID] = notice.Watermark
			effects = append(effects, Effect{Kind: "member_watermark", UserID: notice.UserID, Watermark: notice.Watermark})
		}
		if added {
			out.ActiveMemberCount = len(out.ActiveMemberIDs)
			effects = append(effects, Effect{Kind: "active_member_count", Count: int64(out.ActiveMemberCount)}, Effect{Kind: "active_member_projection_refresh"})
		}
		if changedWatermark {
			effects = append(effects, Effect{Kind: "member_watermark_maintenance", UserID: notice.UserID})
		}
	}
	return Transition{State: out, Applied: true, Effects: effects}
}
func helperSuppressed(s State, userID int64) bool {
	return len(s.ActiveMemberIDs) == 0 || (s.RoomType == 3 && s.Frozen) || s.BotIDs[userID]
}
func containsID(ids []int64, id int64) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}
func copyWatermarks(input map[int64]int64) map[int64]int64 {
	if input == nil {
		return nil
	}
	output := make(map[int64]int64, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneState(s State) State {
	o := s
	o.MemberWatermarks = copyWatermarks(s.MemberWatermarks)
	if s.ActiveMemberIDs != nil {
		o.ActiveMemberIDs = append([]int64{}, s.ActiveMemberIDs...)
	}
	if s.BotIDs != nil {
		o.BotIDs = make(map[int64]bool, len(s.BotIDs))
		for id, bot := range s.BotIDs {
			o.BotIDs[id] = bot
		}
	}
	return o
}
