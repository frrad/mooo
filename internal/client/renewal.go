package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/tokenrefresh"
)

var ErrCredentialRenewal = errors.New("client: credential renewal failed")

func (c *Client) renewCredentials(ctx context.Context) error {
	c.mu.Lock()
	store, state, doer := c.store, c.state, c.http
	c.mu.Unlock()
	if store == nil || state.Credentials == nil {
		return ErrCredentialsAbsent
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return ErrBootstrap
	}
	old := state.Credentials.Clone()
	refreshToken, err := refreshTokenFromMaterial(old.AutoLoginMaterial)
	if err != nil {
		return err
	}
	rotation, err := tokenrefresh.Execute(ctx, doer, tokenrefresh.ClientProfile{
		AppVersion: state.Identity.Metadata.AppVersion, OSVersion: state.Identity.Metadata.OSVersion,
		Language: "en", AccessToken: old.AccessToken, DeviceUUID: wireUUID,
	}, tokenrefresh.Request{RefreshToken: refreshToken})
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
