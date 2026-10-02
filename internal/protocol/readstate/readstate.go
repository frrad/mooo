// Package readstate contains pure read-state transition helpers. It does not
// persist state or issue network requests; callers apply the planned result.
package readstate

const (
	EligibleUnreadExcludedType   int32 = 3
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
	UnreadCount         int64
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
	out := cloneState(input)
	out.ActiveMemberCount = len(out.ActiveMemberIDs)
	out.UnreadCount = input.CountOfNewMessage
	if input.UnreadCount != 0 {
		out.UnreadCount = input.UnreadCount
	}
	if !input.RoomExists {
		return Transition{State: out, Effects: []Effect{}}
	}
	effects := make([]Effect, 0, 7)
	if notice.UserID == input.CurrentUserID {
		if out.UnreadCount > 0 {
			if notice.Watermark < input.LastLogID {
				lower := input.LastSeenLogID
				if lower < notice.Watermark {
					lower = notice.Watermark
				}
				// Query is first by contract; Inputs supplies its selected result.
				effects = append(effects, Effect{Kind: "query_unread", ChatID: notice.ChatID, LowerBound: lower, ExcludedType: EligibleUnreadExcludedType, ExcludedStatus: EligibleUnreadExcludedStatus, AllowedScopes: []int32{1, 3}})
				out.UnreadCount = inputs.EligibleUnreadCount
				effects = append(effects, Effect{Kind: "set_unread", Count: out.UnreadCount})
			} else {
				out.UnreadCount = 0
				effects = append(effects, Effect{Kind: "clear_unread"})
				if out.MentionReplyPresent {
					out.MentionReplyPresent = false
					effects = append(effects, Effect{Kind: "reset_mention_reply"})
				}
			}
		}
		effects = append(effects, Effect{Kind: "check_joined"}, Effect{Kind: "archive_refresh"})
	}
	if !helperSuppressed(out, notice.UserID) {
		if out.MemberWatermarks == nil {
			out.MemberWatermarks = make(map[int64]int64)
		}
		if !containsID(out.ActiveMemberIDs, notice.UserID) {
			out.ActiveMemberIDs = append(out.ActiveMemberIDs, notice.UserID)
			effects = append(effects, Effect{Kind: "active_member_add", UserID: notice.UserID})
			effects = append(effects, Effect{Kind: "active_member_count", Count: int64(len(out.ActiveMemberIDs))})
		}
		out.ActiveMemberCount = len(out.ActiveMemberIDs)
		old, ok := out.MemberWatermarks[notice.UserID]
		if !ok || notice.Watermark > old {
			out.MemberWatermarks[notice.UserID] = notice.Watermark
			effects = append(effects, Effect{Kind: "member_watermark", UserID: notice.UserID, Watermark: notice.Watermark})
		}
	}
	out.CountOfNewMessage = out.UnreadCount
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
	o.ActiveMemberIDs = append([]int64(nil), s.ActiveMemberIDs...)
	o.BotIDs = make(map[int64]bool, len(s.BotIDs))
	for id, bot := range s.BotIDs {
		o.BotIDs[id] = bot
	}
	return o
}
