package sessionlogin

import "errors"

// ErrTokenCursorAssertion reports the observed exceptional input where a
// negative current token is paired with an incoming zero. Callers should
// handle this as a failed selection; the official process-abort effect is not
// reproduced by the clean-room reducer.
var ErrTokenCursorAssertion = errors.New("sessionlogin: token cursor assertion input")

// TokenCursorSelection describes which independent cursor updates were
// selected. It contains no cursor values and performs no storage or profile
// reset work.
type TokenCursorSelection struct {
	TokenUpdate bool
	LBKUpdate   bool
}

// SelectTokenCursor applies the reviewed strict signed comparisons. Equal and
// lower values select no update. A nil incoming cursor is absent and selects
// no update. The negative-current/zero-incoming token assertion is returned as
// an error before either selection is reported.
func SelectTokenCursor(currentToken, incomingToken *int64, currentLBK, incomingLBK *int32) (TokenCursorSelection, error) {
	if currentToken != nil && incomingToken != nil && *currentToken < 0 && *incomingToken == 0 {
		return TokenCursorSelection{}, ErrTokenCursorAssertion
	}
	var selected TokenCursorSelection
	if currentToken != nil && incomingToken != nil && *incomingToken > *currentToken {
		selected.TokenUpdate = true
	}
	if currentLBK != nil && incomingLBK != nil && *incomingLBK > *currentLBK {
		selected.LBKUpdate = true
	}
	return selected, nil
}
