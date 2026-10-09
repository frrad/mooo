package events

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type membershipFeedMember struct {
	UserID   *int64 `json:"userId"`
	UserType int32  `json:"userType"`
}

// The official type-zero chat-log accessor derives its feed from message JSON.
// Its name mapping translates member to leaver and members to invitees.
func membershipMessageMembers(log bson.Raw, added bool) ([]MemberIdentity, error) {
	if hasAnyDuplicateExcept(log) {
		return nil, ErrMalformedEvent
	}
	typ, err := log.LookupErr("type")
	if err != nil || typ.Type != bson.TypeInt32 || typ.Int32() != 0 {
		return nil, ErrMalformedEvent
	}
	value, err := log.LookupErr("message")
	if err != nil || value.Type != bson.TypeString || !validBoundedEventJSON(value.StringValue()) {
		return nil, ErrMalformedEvent
	}
	var feed struct {
		FeedType int32                  `json:"feedType"`
		Member   *membershipFeedMember  `json:"member"`
		Members  []membershipFeedMember `json:"members"`
	}
	if json.Unmarshal([]byte(value.StringValue()), &feed) != nil {
		return nil, ErrMalformedEvent
	}
	selected := feed.Members
	if !added {
		if feed.FeedType != 2 || feed.Member == nil {
			return nil, ErrMalformedEvent
		}
		selected = []membershipFeedMember{*feed.Member}
	} else if feed.FeedType != 1 || len(selected) == 0 {
		return nil, ErrMalformedEvent
	}
	out := make([]MemberIdentity, 0, len(selected))
	seen := map[int64]bool{}
	for _, member := range selected {
		if member.UserID == nil || *member.UserID <= 0 || seen[*member.UserID] {
			return nil, ErrMalformedEvent
		}
		seen[*member.UserID] = true
		out = append(out, MemberIdentity{UserID: *member.UserID, UserType: member.UserType})
	}
	return out, nil
}
