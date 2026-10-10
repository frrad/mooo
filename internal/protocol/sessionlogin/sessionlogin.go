// Package sessionlogin contains the reviewed, transport-independent subset of
// final session login behavior: the LOGINLIST request model and its BSON
// encoding, login status classification, and packet identity formatting. It
// does not send requests, open sockets, or store credentials.
package sessionlogin

import (
	"errors"
	"strings"
)

// LoginListRequest is the semantic LOGINLIST request model. Field types match
// the reviewed wire types; serialization and unset-object behavior remain a
// separate compatibility concern.
type LoginListRequest struct {
	AppVer      string
	OS          string
	Lang        string
	DUUID       string
	SKey        string
	OAuthToken  string
	NType       int32
	MCCMNC      string
	Revision    int32
	DType       int32
	PCST        int32
	BG          bool
	ChatIDs     []int64
	MaxIDs      []int64
	LastTokenID int64
	LBK         int32
}

var (
	ErrMissingRequiredValue   = errors.New("sessionlogin: missing required LOGINLIST value")
	ErrChatListLengthMismatch = errors.New("sessionlogin: chatIds and maxIds lengths differ")
	ErrSKeySet                = errors.New("sessionlogin: sKey must be unset")
)

// Validate enforces only reviewed semantic preconditions. It never inspects
// or transforms the access-token string.
func (r LoginListRequest) Validate() error {
	for _, value := range []string{r.AppVer, r.OS, r.Lang, r.DUUID, r.OAuthToken} {
		if strings.TrimSpace(value) == "" {
			return ErrMissingRequiredValue
		}
	}
	if r.SKey != "" {
		return ErrSKeySet
	}
	if len(r.ChatIDs) != len(r.MaxIDs) {
		return ErrChatListLengthMismatch
	}
	return nil
}

// LoginStatusClass is the fail-closed semantic classification of a LOGINLIST
// response status.
type LoginStatusClass uint8

const (
	LoginStatusUnknown LoginStatusClass = iota
	LoginStatusSuccess
	LoginStatusPartialSuccess
	LoginStatusBlocked
)

// ClassifiedStatus preserves the numeric status even when its meaning is
// unknown.
type ClassifiedStatus struct {
	Code  int32
	Class LoginStatusClass
}

// ClassifyLoginStatus classifies only statuses established by the public
// specification. Unknown values remain non-successful and retain their code.
func ClassifyLoginStatus(code int32) ClassifiedStatus {
	result := ClassifiedStatus{Code: code, Class: LoginStatusUnknown}
	switch code {
	case 0, -305:
		result.Class = LoginStatusSuccess
	case -310:
		result.Class = LoginStatusPartialSuccess
	case -445:
		result.Class = LoginStatusBlocked
	}
	return result
}

// Accepted reports whether the status is one of the two accepted login
// success statuses.
func (s ClassifiedStatus) Accepted() bool { return s.Class == LoginStatusSuccess }
