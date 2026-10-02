package readstate

import "testing"

func TestCountEligibleUnreadUsesReviewedPredicate(t *testing.T) {
	logs := []StoredLog{
		{ChatID: 42, LogID: 9, Type: 1, Status: 1, Scope: 1}, // equality bound
		{ChatID: 42, LogID: 10, Type: 1, Status: 1, Scope: 1},
		{ChatID: 7, LogID: 11, Type: 1, Status: 1, Scope: 1},
		{ChatID: 42, LogID: 0, Type: 1, Status: 1, Scope: 1},
		{ChatID: 42, LogID: -1, Type: 1, Status: 1, Scope: 1},
		{ChatID: 42, LogID: 12, Type: 3, Status: 1, Scope: 1},
		{ChatID: 42, LogID: 16, Type: 10001, Status: 1, Scope: 1},
		{ChatID: 42, LogID: 13, Type: 1, Status: 5, Scope: 1},
		{ChatID: 42, LogID: 14, Type: 1, Status: 1, Scope: 2},
		{ChatID: 42, LogID: 15, Type: 1, Status: 1, Scope: 3},
	}
	if got := CountEligibleUnread(logs, 42, 9); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
	if got := CountEligibleUnread(logs, 42, 14); got != 1 {
		t.Fatalf("count at later bound = %d, want 1", got)
	}
}
