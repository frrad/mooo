package main

import (
	"errors"
	"fmt"
	"testing"

	"maunium.net/go/mautrix"
)

func TestAuthenticationFailureKinds(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errCredentialsMismatch, "credentials_mismatch"},
		{errDeviceMismatch, "device_mismatch"},
		{errCredentialsUnavailable, "credentials_unavailable"},
		{errLoginIdentityMismatch, "login_identity_mismatch"},
		{fmt.Errorf("login: %w", mautrix.MLimitExceeded), "credentials_rate_limited"},
		{fmt.Errorf("login: %w", mautrix.MForbidden), "credentials_forbidden"},
		{errors.New("private directory required"), "credentials"},
	}
	for _, c := range cases {
		if got := exitKind(authenticationFailure(c.err)); got != c.want {
			t.Errorf("exitKind(authenticationFailure(%v)) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestExitKindsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, sentinel := range exitKinds {
		kind := exitKind(sentinel)
		if kind != sentinel.Error() {
			t.Errorf("exitKind(%v) = %q, want its own kind", sentinel, kind)
		}
		if seen[kind] {
			t.Errorf("duplicate exit kind %q", kind)
		}
		seen[kind] = true
	}
	if got := exitKind(errors.New("unmapped")); got != "invalid" {
		t.Errorf("unmapped error kind = %q, want invalid", got)
	}
}
