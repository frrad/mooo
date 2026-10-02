package sessionlogin

import (
	"errors"
	"testing"
)

func TestSelectTokenCursorStrictIndependentSignedComparisons(t *testing.T) {
	int64p := func(v int64) *int64 { return &v }
	int32p := func(v int32) *int32 { return &v }
	cases := []struct {
		name        string
		current     *int64
		incoming    *int64
		currentLBK  *int32
		incomingLBK *int32
		wantToken   bool
		wantLBK     bool
	}{
		{name: "greater token", current: int64p(41), incoming: int64p(42), currentLBK: int32p(7), incomingLBK: int32p(7), wantToken: true},
		{name: "equal token", current: int64p(42), incoming: int64p(42), wantToken: false},
		{name: "lower token", current: int64p(42), incoming: int64p(41), wantToken: false},
		{name: "greater lbk independently", current: int64p(42), incoming: int64p(42), currentLBK: int32p(7), incomingLBK: int32p(8), wantLBK: true},
		{name: "lower lbk independently", current: int64p(42), incoming: int64p(42), currentLBK: int32p(8), incomingLBK: int32p(7), wantLBK: false},
		{name: "negative token domain", current: int64p(-2), incoming: int64p(-1), wantToken: true},
		{name: "negative lbk domain", currentLBK: int32p(-2), incomingLBK: int32p(-1), wantLBK: true},
		{name: "positive to negative token", current: int64p(1), incoming: int64p(-1), wantToken: false},
		{name: "absent incoming values", current: int64p(1), currentLBK: int32p(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectTokenCursor(tc.current, tc.incoming, tc.currentLBK, tc.incomingLBK)
			if err != nil {
				t.Fatal(err)
			}
			if got.TokenUpdate != tc.wantToken || got.LBKUpdate != tc.wantLBK {
				t.Fatalf("selection=%+v want token=%v lbk=%v", got, tc.wantToken, tc.wantLBK)
			}
		})
	}
}

func TestSelectTokenCursorAssertionIsGoError(t *testing.T) {
	current, incoming := int64(-1), int64(0)
	got, err := SelectTokenCursor(&current, &incoming, nil, nil)
	if !errors.Is(err, ErrTokenCursorAssertion) {
		t.Fatalf("error=%v want ErrTokenCursorAssertion", err)
	}
	if got != (TokenCursorSelection{}) {
		t.Fatalf("selection=%+v want zero selection", got)
	}
}

func TestSelectTokenCursorDoesNotMutateInputs(t *testing.T) {
	current, incoming := int64(41), int64(42)
	currentLBK, incomingLBK := int32(7), int32(8)
	_, err := SelectTokenCursor(&current, &incoming, &currentLBK, &incomingLBK)
	if err != nil {
		t.Fatal(err)
	}
	if current != 41 || incoming != 42 || currentLBK != 7 || incomingLBK != 8 {
		t.Fatalf("inputs mutated: token=%d/%d lbk=%d/%d", current, incoming, currentLBK, incomingLBK)
	}
}
