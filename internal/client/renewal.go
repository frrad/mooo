package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/tokenrefresh"
)

var ErrCredentialRenewal = errors.New("client: credential renewal failed")

// renewRejected renews the credentials after the server rejected the access
// token in rejected. Renewals are serialized: when another caller has already
// replaced that token, the rejection is answered and no second renewal is
// sent. A renewal that KakaoTalk ends with -998 or -999 reports ErrSignedOut.
func (c *Client) renewRejected(ctx context.Context, rejected authstate.State) error {
	c.renewMu.Lock()
	defer c.renewMu.Unlock()
	c.mu.Lock()
	current := c.state.Credentials
	c.mu.Unlock()
	if rejected.Credentials != nil && current != nil && current.AccessToken != rejected.Credentials.AccessToken {
		return nil
	}
	if err := c.renewCredentials(ctx); err != nil {
		var status tokenrefresh.RejectedError
		if errors.As(err, &status) && (status.Status == -998 || status.Status == -999) {
			return errors.Join(ErrCredentialRenewal, ErrSignedOut, err)
		}
		return errors.Join(ErrCredentialRenewal, err)
	}
	return nil
}

// webCall runs one authenticated web API operation with a fresh credential
// snapshot. An HTTP 401 rejects the access token while the LOCO session may
// stay healthy, so, as a -950 LOGINLIST does at connect, it renews the
// credentials and runs the operation once more with the replacement token. A
// 401 means the server refused the request, so even a mutation is repeated at
// most once and never after an ambiguous outcome. A failed renewal is joined
// to the 401.
func webCall[T any](c *Client, ctx context.Context, op func(macweb.Doer, authstate.State) (T, error)) (T, error) {
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	result, err := op(doer, state)
	var status *macweb.StatusError
	if err == nil || !errors.As(err, &status) || status.Code != http.StatusUnauthorized {
		return result, err
	}
	c.mu.Lock()
	store := c.store
	c.mu.Unlock()
	if store == nil {
		return result, err
	}
	if renewErr := c.renewRejected(ctx, state); renewErr != nil {
		return result, errors.Join(err, renewErr)
	}
	doer, state, err = c.httpProfile(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return op(doer, state)
}

func (c *Client) renewCredentials(ctx context.Context) error {
	c.mu.Lock()
	store, state, doer := c.store, c.state, c.http
	c.mu.Unlock()
	if store == nil || state.Credentials == nil {
		return errCredentialsAbsent
	}
	profile, err := webProfile(state)
	if err != nil {
		return err
	}
	old := state.Credentials.Clone()
	refreshToken, err := refreshTokenFromMaterial(old.AutoLoginMaterial)
	if err != nil {
		return err
	}
	rotation, err := tokenrefresh.Execute(ctx, doer, profile, tokenrefresh.Request{RefreshToken: refreshToken})
	if err != nil {
		return err
	}
	material, err := rotateAutoLoginMaterial(old.AutoLoginMaterial, rotation)
	if err != nil {
		return err
	}
	replacement := authstate.Credentials{
		UserID: old.UserID, AccessToken: rotation.AccessToken, AutoLoginMaterial: material,
	}
	if err := store.CompareAndSwapCredentials(old, replacement); err != nil {
		return err
	}
	copy := replacement.Clone()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	c.state.Credentials = &copy
	c.mu.Unlock()
	return nil
}

func refreshTokenFromMaterial(material []byte) (string, error) {
	object, err := decodeAutoLoginMaterial(material)
	if err != nil {
		return "", err
	}
	var refreshToken string
	if raw, ok := object["refresh_token"]; !ok || json.Unmarshal(raw, &refreshToken) != nil || strings.TrimSpace(refreshToken) == "" {
		return "", ErrCredentialRenewal
	}
	return refreshToken, nil
}

func rotateAutoLoginMaterial(material []byte, rotation tokenrefresh.Rotation) ([]byte, error) {
	object, err := decodeAutoLoginMaterial(material)
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]string{
		"refresh_token": rotation.RefreshToken,
		"token_type":    rotation.TokenType,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, ErrCredentialRenewal
		}
		object[key] = encoded
	}
	updated, err := json.Marshal(object)
	if err != nil {
		return nil, ErrCredentialRenewal
	}
	return updated, nil
}

func decodeAutoLoginMaterial(material []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(material) == 0 || json.Unmarshal(material, &object) != nil || object == nil {
		return nil, ErrCredentialRenewal
	}
	return object, nil
}
