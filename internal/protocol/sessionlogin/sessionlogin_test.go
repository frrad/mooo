package sessionlogin

import (
	"errors"
	"testing"
)

func TestLoginListRequestValidationAndWireTypes(t *testing.T) {
	valid := LoginListRequest{
		AppVer:      "synthetic-client",
		OS:          "synthetic-os",
		Lang:        "en",
		DUUID:       "synthetic-device",
		OAuthToken:  "synthetic-access",
		NType:       int32(7),
		MCCMNC:      "synthetic-network",
		Revision:    int32(8),
		DType:       int32(9),
		PCST:        int32(10),
		BG:          true,
		ChatIDs:     []int64{11, 12},
		MaxIDs:      []int64{21, 22},
		LastTokenID: int64(31),
		LBK:         int32(41),
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		edit func(*LoginListRequest)
		want error
	}{
		{"missing app version", func(r *LoginListRequest) { r.AppVer = "" }, ErrMissingRequiredValue},
		{"missing operating system", func(r *LoginListRequest) { r.OS = " " }, ErrMissingRequiredValue},
		{"missing language", func(r *LoginListRequest) { r.Lang = "" }, ErrMissingRequiredValue},
		{"missing device identity", func(r *LoginListRequest) { r.DUUID = "" }, ErrMissingRequiredValue},
		{"missing access token", func(r *LoginListRequest) { r.OAuthToken = "" }, ErrMissingRequiredValue},
		{"sKey set", func(r *LoginListRequest) { r.SKey = "unexpected" }, ErrSKeySet},
		{"chat list mismatch", func(r *LoginListRequest) { r.MaxIDs = r.MaxIDs[:1] }, ErrChatListLengthMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.edit(&request)
			if !errors.Is(request.Validate(), test.want) {
				t.Fatalf("Validate() = %v, want %v", request.Validate(), test.want)
			}
		})
	}

	emptyLists := valid
	emptyLists.ChatIDs = nil
	emptyLists.MaxIDs = []int64{}
	emptyLists.MCCMNC = ""
	if err := emptyLists.Validate(); err != nil {
		t.Fatalf("empty optional environment field or paired lists rejected: %v", err)
	}
}

func TestClassifyLoginStatus(t *testing.T) {
	tests := []struct {
		code     int32
		class    LoginStatusClass
		accepted bool
	}{
		{0, LoginStatusSuccess, true},
		{-305, LoginStatusSuccess, true},
		{-310, LoginStatusPartialSuccess, false},
		{-445, LoginStatusBlocked, false},
		{17, LoginStatusUnknown, false},
		{-999, LoginStatusUnknown, false},
	}
	for _, test := range tests {
		t.Run(statusName(test.code), func(t *testing.T) {
			classified := ClassifyLoginStatus(test.code)
			if classified.Code != test.code || classified.Class != test.class || classified.Accepted() != test.accepted {
				t.Fatalf("classified = %#v accepted=%v", classified, classified.Accepted())
			}
		})
	}
}

func statusName(code int32) string {
	if code < 0 {
		return "negative"
	}
	return "status"
}
